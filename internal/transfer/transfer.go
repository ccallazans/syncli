package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ccallazans/syncli/internal/discovery"
	"github.com/ccallazans/syncli/internal/explog"
	"github.com/ccallazans/syncli/internal/watcher"
)

// opName converte um opcode do protocolo em uma string curta para o log de experimentos.
func opName(op byte) string {
	switch op {
	case opWrite:
		return "write"
	case opRemove:
		return "remove"
	case opDelta:
		return "delta"
	default:
		return fmt.Sprintf("0x%02x", op)
	}
}

// Opcodes do protocolo de comunicação.
//
// Layout do quadro (cabeçalho fixo de 13 bytes):
//
//	[op: 1 byte][path_len: 4 bytes BE][payload_len: 8 bytes BE][bytes do path][bytes do payload]
//
// op é uint8 — a menor unidade endereçável, sem necessidade de padding.
// path_len é uint32 (e não uint16) para alinhar o cabeçalho a uma fronteira de 4 bytes e
// evitar casos extremos com sistemas de arquivos que permitem caminhos muito longos.
// payload_len é uint64 para suportar arquivos de até 16 EB; uint32 (limite de 4 GB) seria
// insuficiente para mídias grandes ou backups comprimidos.
// O big-endian (ordem de bytes de rede, RFC 1700) é determinístico entre x86 (little-endian)
// e ARM/SPARC (big-endian); implementações futuras decodificam sem troca de bytes.
//
// opWrite   payload = conteúdo bruto do arquivo
// opRemove  payload = vazio (payload_len = 0)
// opDelta   payload = delta serializado (ver serializeDelta)
// opSnapshotReq/End usam o mesmo quadro com path_len=0 e payload_len=0.
const (
	opWrite  = byte(watcher.OpWrite)  // 0x01 — conteúdo completo do arquivo
	opRemove = byte(watcher.OpRemove) // 0x02 — apaga o arquivo
	opDelta  = byte(0x03)             // atualização diferencial (apenas blocos alterados)

	// Valores altos de opcode (0xFE–0xFF) separam mensagens de controle dos opcodes de
	// operação (0x01–0x03). Usar 0xFF como terminador de stream é um idioma comum de
	// protocolos binários.
	opSnapshotReq = byte(0xFE) // solicita snapshot completo do diretório
	opSnapshotEnd = byte(0xFF) // fim do stream de snapshot

	// ACK ASCII (0x06), o caractere convencional de confirmação positiva desde BSC/HDLC.
	// A semântica é inequívoca: "recebido e aplicado com sucesso".
	ackByte = byte(0x06)

	// Conexões em LAN normalmente se estabelecem em menos de 1ms; 10s acomodam sistemas
	// sobrecarregados. Um valor maior causaria bloqueios longos em peers que se desconectaram
	// silenciosamente.
	dialTimeout = 10 * time.Second

	// Cobre todo o pipeline — transmissão do payload + desserialização do delta + escrita em
	// disco no receptor. Armazenamento lento (HDD, montagens NFS) pode ser lento para arquivos grandes.
	transferTimeout = 30 * time.Second

	// 500ms precisa exceder o debounce (200ms) + a latência de notificação do fsnotify para o
	// arquivo que o receptor acabou de escrever. Essa margem previne o eco (re-sincronizar um
	// arquivo que acabamos de receber) sem bloquear eventos legítimos subsequentes no mesmo caminho.
	excludeTTL = 500 * time.Millisecond

	// 64 KB equilibra o overhead de cabeçalho por bloco (12 B: 8 B de offset + 4 B de data_len,
	// proporção = 0,018%) contra a granularidade do delta para arquivos de texto e binários
	// típicos. Arquivos menores que blockSize são sempre enviados como opWrite, pois o payload
	// do delta excederia o próprio conteúdo do arquivo.
	blockSize = 64 * 1024

	// lwwHeaderSize é o cabeçalho fixo prefixado a todo payload de arquivo no protocolo para
	// suportar a resolução determinística "último a escrever vence" (last-write-wins):
	//   [version_ns: 8 B BE][content_sha256: 32 B]
	// O receptor compara remote.version com o mtime do arquivo local; empates são resolvidos
	// pela comparação lexicográfica do sha256 do conteúdo final. Snapshot, opWrite, opDelta e
	// opRemove carregam este cabeçalho; os marcadores de stream de snapshot
	// (opSnapshotReq/opSnapshotEnd) mantêm payload_len = 0.
	lwwHeaderSize = 8 + sha256.Size
)

// blockHash é um digest SHA-256 de um bloco de 64KB.
type blockHash = [sha256.Size]byte

// DeltaBlock representa um único bloco alterado a ser aplicado em um dado offset.
type DeltaBlock struct {
	Offset int64
	Data   []byte
}

// outboundMsg é uma mensagem pré-construída pronta para ser enviada a qualquer número de peers.
//
// Construir a mensagem uma única vez antes do fan-out evita uma condição de corrida no cache de
// hashes. Se cada goroutine de envio construísse seu próprio delta, a primeira a terminar
// atualizaria o cache, fazendo as goroutines seguintes computarem deltas inconsistentes.
type outboundMsg struct {
	op      byte
	path    string
	version int64    // ns desde a epoch — mtime do arquivo capturado em buildMsg
	hash    [32]byte // sha256 do conteúdo final esperado (após aplicar a operação)
	payload []byte   // conteúdo bruto (opWrite) ou delta serializado (opDelta); nil para opRemove
}

// Manager cuida do envio de eventos de arquivo aos peers e do recebimento de eventos deles.
type Manager struct {
	dir    string
	port   int
	disc   *discovery.Discovery
	events <-chan watcher.Event

	excludeMu sync.RWMutex
	excluded  map[string]time.Time

	// O cache de hashes existe apenas em memória — não é persistido entre reinícios. A primeira
	// transferência de cada arquivo após a inicialização sempre usa opWrite (conteúdo completo).
	// Arquivos recebidos via snapshot pré-populam o cache para que sua primeira modificação
	// subsequente possa usar opDelta diretamente, sem um envio completo.
	cacheMu sync.RWMutex
	cache   map[string][]blockHash
}

// New cria um novo Manager de transferência.
func New(dir string, port int, disc *discovery.Discovery, events <-chan watcher.Event) *Manager {
	return &Manager{
		dir:      dir,
		port:     port,
		disc:     disc,
		events:   events,
		excluded: make(map[string]time.Time),
		cache:    make(map[string][]blockHash),
	}
}

// Start inicia o servidor TCP e o loop de sincronização de saída.
func (m *Manager) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", m.port))
	if err != nil {
		return fmt.Errorf("transfer: listen on :%d: %w", m.port, err)
	}

	slog.Info("TCP server listening", "port", m.port)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	go m.acceptLoop(ctx, ln)
	go m.sendLoop(ctx)

	return nil
}

// ── Entrada (servidor) ────────────────────────────────────────────────────────

func (m *Manager) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				slog.Error("transfer: accept error", "err", err)
				continue
			}
		}
		slog.Info("inbound connection", "remote", conn.RemoteAddr())
		go m.handleConn(conn)
	}
}

// handleConn despacha com base no primeiro opcode:
// requisição de snapshot → serve o diretório completo; caso contrário → aplica eventos de arquivo.
func (m *Manager) handleConn(conn net.Conn) {
	defer conn.Close()

	op, relPath, payload, err := readMessage(conn)
	if err != nil {
		if err != io.EOF {
			slog.Debug("transfer: read first message", "remote", conn.RemoteAddr(), "err", err)
		}
		return
	}

	if op == opSnapshotReq {
		m.serveSnapshot(conn)
		return
	}

	m.applyAndAck(conn, op, relPath, payload)
	for {
		op, relPath, payload, err = readMessage(conn)
		if err != nil {
			if err != io.EOF {
				slog.Debug("transfer: read message", "remote", conn.RemoteAddr(), "err", err)
			}
			return
		}
		m.applyAndAck(conn, op, relPath, payload)
	}
}

func (m *Manager) applyAndAck(conn net.Conn, op byte, relPath string, payload []byte) {
	absPath := filepath.Join(m.dir, relPath)

	remoteVersion, remoteHash, body, err := unwrapLWW(payload)
	if err != nil {
		slog.Error("transfer: invalid LWW header", "path", relPath, "err", err)
		// Mesmo assim confirmamos com ACK para manter o protocolo consistente — o remetente
		// espera exatamente um byte de ACK após cada mensagem.
		m.sendAck(conn, relPath, op)
		return
	}

	apply, reason := m.decideApply(absPath, op, remoteVersion, remoteHash)
	if !apply {
		slog.Info("LWW reject", "path", relPath, "op", opName(op),
			"reason", reason, "remote_version", remoteVersion)
		explog.EmitFields("apply_rejected", map[string]any{
			"path":           relPath,
			"op":             opName(op),
			"reason":         reason,
			"remote_version": remoteVersion,
			"peer":           conn.RemoteAddr().String(),
		})
		m.sendAck(conn, relPath, op)
		return
	}

	// Ramo de aplicação — somente aqui marcamos o caminho como excluído, para que o fs_event
	// gerado pela nossa própria escrita não seja re-propagado.
	m.exclude(relPath)

	switch op {
	case opWrite:
		if err := writeFileLWW(absPath, body, remoteVersion); err != nil {
			slog.Error("transfer: apply full write", "path", relPath, "err", err)
		} else {
			slog.Info("file received (full)", "from", conn.RemoteAddr(), "path", relPath, "bytes", len(body))
			explog.Emit("apply", relPath, opName(op), len(body))
		}

	case opDelta:
		totalSize, blocks, err := deserializeDelta(body)
		if err != nil {
			slog.Error("transfer: deserialize delta", "path", relPath, "err", err)
		} else if err := applyDeltaLWW(absPath, totalSize, blocks, remoteVersion); err != nil {
			slog.Error("transfer: apply delta", "path", relPath, "err", err)
		} else {
			slog.Info("file received (delta)", "from", conn.RemoteAddr(), "path", relPath,
				"blocks", len(blocks), "delta_bytes", len(body))
			explog.Emit("apply", relPath, opName(op), len(body))
		}

	case opRemove:
		if err := removeFileLWW(absPath); err != nil {
			slog.Error("transfer: apply remove", "path", relPath, "err", err)
		} else {
			slog.Info("file removed", "from", conn.RemoteAddr(), "path", relPath)
			explog.Emit("apply", relPath, opName(op), 0)
		}
	}

	m.sendAck(conn, relPath, op)
}

// decideApply aplica a política de resolução LWW a uma operação remota.
//
// Para opWrite/opDelta: usa shouldApply (mtime + desempate por sha256).
// Para opRemove: uma remoção remota só vence um arquivo local se sua versão (instante de captura
// no remetente) for estritamente maior que o mtime local; em empates o arquivo local prevalece
// (seu conteúdo nunca tem prioridade menor que nada).
func (m *Manager) decideApply(absPath string, op byte, remoteVersion int64, remoteHash [32]byte) (bool, string) {
	if op == opRemove {
		info, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, "no_local" // já não existe, nada a fazer
			}
			return false, "stat_error"
		}
		localVersion := info.ModTime().UnixNano()
		if remoteVersion > localVersion {
			return true, "newer_remote"
		}
		return false, "older_remote"
	}
	return shouldApply(absPath, remoteVersion, remoteHash)
}

func (m *Manager) sendAck(conn net.Conn, relPath string, op byte) {
	explog.EmitFields("ack_send", map[string]any{
		"path": relPath,
		"op":   opName(op),
		"peer": conn.RemoteAddr().String(),
	})
	if _, err := conn.Write([]byte{ackByte}); err != nil {
		slog.Debug("transfer: send ack", "err", err)
	}
}

// ── Servidor de snapshot ───────────────────────────────────────────────────────

func (m *Manager) serveSnapshot(conn net.Conn) {
	slog.Info("serving snapshot", "to", conn.RemoteAddr())
	explog.EmitFields("snapshot_serve_start", map[string]any{
		"peer": conn.RemoteAddr().String(),
	})

	var fileCount int
	var totalBytes int64

	err := filepath.WalkDir(m.dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(m.dir, path)
		if err != nil {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			slog.Warn("snapshot: skipping unreadable info", "path", rel, "err", err)
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("snapshot: skipping unreadable file", "path", rel, "err", err)
			return nil
		}

		// Envolve com o cabeçalho LWW para que o nó receptor possa comparar com qualquer cópia
		// local que já tenha (ex.: arquivos anteriores à entrada no grupo).
		version := info.ModTime().UnixNano()
		hash := sha256.Sum256(content)
		wirePayload := wrapLWW(version, hash, content)

		if err := writeMessage(conn, opWrite, rel, wirePayload); err != nil {
			return fmt.Errorf("send %s: %w", rel, err)
		}

		fileCount++
		totalBytes += int64(len(content))
		slog.Debug("snapshot: sent file", "path", rel, "bytes", len(content), "version", version)
		return nil
	})

	if err != nil {
		slog.Error("snapshot: walk error", "to", conn.RemoteAddr(), "err", err)
		return
	}

	if err := writeMessage(conn, opSnapshotEnd, "", nil); err != nil {
		slog.Error("snapshot: send end marker", "err", err)
		return
	}

	slog.Info("snapshot served", "to", conn.RemoteAddr(), "files", fileCount, "bytes", totalBytes)
	explog.EmitFields("snapshot_serve_end", map[string]any{
		"peer":  conn.RemoteAddr().String(),
		"files": fileCount,
		"bytes": totalBytes,
	})
}

// ── Cliente de snapshot ────────────────────────────────────────────────────────

// RequestSnapshot conecta-se a um peer e baixa todos os arquivos dele.
func (m *Manager) RequestSnapshot(peer discovery.Peer) error {
	slog.Info("requesting full snapshot", "from", peer.Addr)
	explog.EmitFields("snapshot_req", map[string]any{
		"peer": peer.Addr,
	})

	conn, err := net.DialTimeout("tcp", peer.Addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("dial %s: %w", peer.Addr, err)
	}
	defer conn.Close()

	if err := writeMessage(conn, opSnapshotReq, "", nil); err != nil {
		return fmt.Errorf("send snapshot request: %w", err)
	}

	var fileCount int
	var totalBytes int64

	for {
		op, relPath, payload, err := readMessage(conn)
		if err != nil {
			return fmt.Errorf("receive snapshot: %w", err)
		}
		if op == opSnapshotEnd {
			break
		}
		if op != opWrite {
			slog.Warn("snapshot: unexpected opcode", "op", fmt.Sprintf("0x%02X", op))
			continue
		}

		remoteVersion, remoteHash, content, err := unwrapLWW(payload)
		if err != nil {
			slog.Error("snapshot: invalid LWW header", "path", relPath, "err", err)
			continue
		}

		absPath := filepath.Join(m.dir, relPath)

		// LWW: ignora arquivos cuja cópia local é mais nova que a versão do snapshot.
		apply, reason := shouldApply(absPath, remoteVersion, remoteHash)
		if !apply {
			slog.Info("snapshot: LWW reject", "path", relPath, "reason", reason,
				"remote_version", remoteVersion)
			explog.EmitFields("apply_rejected", map[string]any{
				"path":           relPath,
				"op":             "snapshot",
				"reason":         reason,
				"remote_version": remoteVersion,
				"peer":           peer.Addr,
			})
			continue
		}

		m.exclude(relPath)

		if err := writeFileLWW(absPath, content, remoteVersion); err != nil {
			slog.Error("snapshot: write file", "path", relPath, "err", err)
			continue
		}

		// Semeia o cache de hashes de blocos para que a primeira mudança real após o snapshot
		// possa usar transferência diferencial em vez de enviar o arquivo completo.
		m.cacheMu.Lock()
		m.cache[relPath] = hashBlocks(content)
		m.cacheMu.Unlock()

		fileCount++
		totalBytes += int64(len(content))
		slog.Info("snapshot: received file", "path", relPath, "bytes", len(content))
		explog.Emit("apply", relPath, "write", len(content))
	}

	slog.Info("snapshot complete", "from", peer.Addr, "files", fileCount, "bytes", totalBytes)
	explog.EmitFields("snapshot_end", map[string]any{
		"peer":  peer.Addr,
		"files": fileCount,
		"bytes": totalBytes,
	})
	return nil
}

// ── Saída (cliente) ────────────────────────────────────────────────────────────

func (m *Manager) sendLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-m.events:
			if !ok {
				return
			}
			if m.isExcluded(ev.Path) {
				slog.Debug("skipping excluded path (echo prevention)", "path", ev.Path)
				continue
			}
			explog.Emit("detect", ev.Path, ev.Op.String(), 0)
			m.fanOut(ev)
		}
	}
}

// fanOut constrói a mensagem de saída uma única vez e a envia a todos os peers concorrentemente.
// Construir uma só vez garante que todos os peers recebam o mesmo delta e evita reler o arquivo
// ou recalcular hashes para cada peer.
func (m *Manager) fanOut(ev watcher.Event) {
	peers := m.disc.GetPeers()
	if len(peers) == 0 {
		slog.Debug("no peers available, event dropped", "path", ev.Path)
		return
	}

	msg, err := m.buildMsg(ev)
	if err != nil {
		slog.Error("failed to build outbound message", "path", ev.Path, "err", err)
		return
	}

	for _, p := range peers {
		go func(peer discovery.Peer) {
			if err := m.sendMsg(peer, msg); err != nil {
				slog.Warn("sync failed", "peer", peer.Addr, "path", ev.Path, "err", err)
			}
		}(p)
	}
}

// buildMsg lê o arquivo, decide entre transferência completa e diferencial, atualiza o cache de
// hashes de blocos, captura a versão LWW (mtime do arquivo) e o sha256 do conteúdo final, e
// retorna uma mensagem pronta para envio.
func (m *Manager) buildMsg(ev watcher.Event) (outboundMsg, error) {
	if ev.Op == watcher.OpRemove {
		m.cacheMu.Lock()
		delete(m.cache, ev.Path)
		m.cacheMu.Unlock()
		// Em remoções não há arquivo para stat — usa-se o relógio do sistema como versão. O hash
		// é todo zeros (sem conteúdo). O receptor compara com o mtime local; se o mtime local for
		// mais recente, o arquivo local é preservado.
		return outboundMsg{
			op:      opRemove,
			path:    ev.Path,
			version: time.Now().UnixNano(),
			hash:    [32]byte{},
		}, nil
	}

	// OpWrite: lê o conteúdo atual e captura o mtime via stat.
	absPath := filepath.Join(m.dir, ev.Path)
	info, err := os.Stat(absPath)
	if err != nil {
		return outboundMsg{}, fmt.Errorf("stat %s: %w", ev.Path, err)
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		return outboundMsg{}, fmt.Errorf("read %s: %w", ev.Path, err)
	}

	version := info.ModTime().UnixNano()
	contentHash := sha256.Sum256(content)

	newHashes := hashBlocks(content)

	m.cacheMu.RLock()
	oldHashes, hasCached := m.cache[ev.Path]
	m.cacheMu.RUnlock()

	msg := outboundMsg{
		path:    ev.Path,
		version: version,
		hash:    contentHash,
	}

	if hasCached {
		blocks := computeDelta(oldHashes, content)

		if len(blocks) == 0 {
			// O conteúdo do arquivo é idêntico ao último enviado — pula.
			slog.Debug("file unchanged since last send, skipping", "path", ev.Path)
			return outboundMsg{}, fmt.Errorf("skip: no changes") // o chamador irá ignorar
		}

		deltaPayload := serializeDelta(int64(len(content)), blocks)

		if len(deltaPayload) < len(content) {
			reduction := 100.0 * (1.0 - float64(len(deltaPayload))/float64(len(content)))
			slog.Info("differential transfer", "path", ev.Path,
				"full_bytes", len(content),
				"delta_bytes", len(deltaPayload),
				"reduction", fmt.Sprintf("%.0f%%", reduction),
				"changed_blocks", len(blocks))
			msg.op = opDelta
			msg.payload = deltaPayload
		} else {
			// Todos os blocos mudaram (ex.: o arquivo foi totalmente reescrito). Envio completo.
			slog.Info("full transfer (all blocks changed)", "path", ev.Path, "bytes", len(content))
			msg.op = opWrite
			msg.payload = content
		}
	} else {
		// Sem versão em cache — é um arquivo novo ou o primeiro envio. Transferência completa.
		slog.Info("full transfer (new file)", "path", ev.Path, "bytes", len(content))
		msg.op = opWrite
		msg.payload = content
	}

	// Atualiza o cache com os hashes de blocos atuais.
	// Feito ANTES de lançar as goroutines para que todos os peers usem um snapshot consistente.
	m.cacheMu.Lock()
	m.cache[ev.Path] = newHashes
	m.cacheMu.Unlock()

	return msg, nil
}

func (m *Manager) sendMsg(peer discovery.Peer, msg outboundMsg) error {
	conn, err := net.DialTimeout("tcp", peer.Addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("dial %s: %w", peer.Addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(transferTimeout))

	// Envolve o corpo com o cabeçalho LWW (versão + sha256) para que o receptor possa fazer a
	// resolução determinística de conflitos.
	wirePayload := wrapLWW(msg.version, msg.hash, msg.payload)

	bytesWire := 13 + len(msg.path) + len(wirePayload)
	explog.EmitFields("send_start", map[string]any{
		"path":       msg.path,
		"op":         opName(msg.op),
		"bytes":      len(msg.payload),
		"bytes_wire": bytesWire,
		"version":    msg.version,
		"peer":       peer.Addr,
	})

	if err := writeMessage(conn, msg.op, msg.path, wirePayload); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return fmt.Errorf("ack: %w", err)
	}

	explog.EmitFields("ack_recv", map[string]any{
		"path": msg.path,
		"op":   opName(msg.op),
		"peer": peer.Addr,
	})

	switch msg.op {
	case opWrite:
		slog.Info("file sent (full)", "peer", peer.Addr, "path", msg.path, "bytes", len(msg.payload))
	case opDelta:
		slog.Info("file sent (delta)", "peer", peer.Addr, "path", msg.path, "bytes", len(msg.payload))
	case opRemove:
		slog.Info("delete sent", "peer", peer.Addr, "path", msg.path)
	}
	explog.EmitFields("send", map[string]any{
		"path":       msg.path,
		"op":         opName(msg.op),
		"bytes":      len(msg.payload),
		"bytes_wire": bytesWire,
		"version":    msg.version,
		"peer":       peer.Addr,
	})

	return nil
}

// ── Transferência diferencial ──────────────────────────────────────────────────

// hashBlocks divide o conteúdo em pedaços de blockSize e retorna um hash SHA-256 para cada
// pedaço. Retorna nil para conteúdo vazio.
//
// O SHA-256 oferece resistência a colisões que MD5/CRC32 não garantem. Blocos idênticos
// produzem hashes idênticos; uma divergência significa que o conteúdo mudou. O crypto/sha256
// faz parte da biblioteca padrão do Go — sem dependência externa.
func hashBlocks(content []byte) []blockHash {
	if len(content) == 0 {
		return nil
	}
	n := (len(content) + blockSize - 1) / blockSize
	hashes := make([]blockHash, n)
	for i := range hashes {
		start := i * blockSize
		end := min(start+blockSize, len(content))
		hashes[i] = sha256.Sum256(content[start:end])
	}
	return hashes
}

// computeDelta compara os hashes de blocos antigos com o novo conteúdo do arquivo e retorna
// apenas os blocos que mudaram ou são novos (além do tamanho do arquivo antigo).
func computeDelta(oldHashes []blockHash, newContent []byte) []DeltaBlock {
	newHashes := hashBlocks(newContent)
	var blocks []DeltaBlock

	for i, h := range newHashes {
		var old blockHash
		if i < len(oldHashes) {
			old = oldHashes[i]
		}
		// Um blockHash zerado significa "nenhum bloco anterior" — sempre incluído.
		if h != old {
			start := int64(i) * blockSize
			end := min(int(start)+blockSize, len(newContent))
			blocks = append(blocks, DeltaBlock{
				Offset: start,
				Data:   newContent[start:end],
			})
		}
	}
	// Blocos além de newHashes (o arquivo encolheu) são tratados via Truncate no receptor.
	return blocks
}

// serializeDelta codifica o delta no payload do protocolo para opDelta.
//
// Formato:
//
//	[total_size: 8B BE]
//	[block_count: 4B BE]
//	para cada bloco:
//	  [offset: 8B BE][data_len: 4B BE][bytes de dados]
//
// total_size (8 B) permite ao receptor chamar Truncate() corretamente quando o arquivo encolheu;
// sem ele, seria necessária uma chamada stat separada ou um opcode de truncamento dedicado.
// block_count (4 B, uint32) cobre ~4 bilhões de blocos; a 64 KB/bloco isso são 256 TB de dados
// modificados por delta — muito além de qualquer limitação prática.
func serializeDelta(totalSize int64, blocks []DeltaBlock) []byte {
	size := 8 + 4
	for _, b := range blocks {
		size += 8 + 4 + len(b.Data)
	}

	buf := make([]byte, size)
	pos := 0

	binary.BigEndian.PutUint64(buf[pos:], uint64(totalSize))
	pos += 8
	binary.BigEndian.PutUint32(buf[pos:], uint32(len(blocks)))
	pos += 4

	for _, b := range blocks {
		binary.BigEndian.PutUint64(buf[pos:], uint64(b.Offset))
		pos += 8
		binary.BigEndian.PutUint32(buf[pos:], uint32(len(b.Data)))
		pos += 4
		copy(buf[pos:], b.Data)
		pos += len(b.Data)
	}

	return buf
}

// deserializeDelta decodifica o payload do protocolo de volta em blocos de delta estruturados.
func deserializeDelta(payload []byte) (totalSize int64, blocks []DeltaBlock, err error) {
	if len(payload) < 12 {
		return 0, nil, fmt.Errorf("payload too short (%d bytes)", len(payload))
	}

	pos := 0
	totalSize = int64(binary.BigEndian.Uint64(payload[pos:]))
	pos += 8
	blockCount := binary.BigEndian.Uint32(payload[pos:])
	pos += 4

	blocks = make([]DeltaBlock, blockCount)
	for i := range blocks {
		if pos+12 > len(payload) {
			return 0, nil, fmt.Errorf("truncated at block %d header", i)
		}
		blocks[i].Offset = int64(binary.BigEndian.Uint64(payload[pos:]))
		pos += 8
		dataLen := int(binary.BigEndian.Uint32(payload[pos:]))
		pos += 4
		if pos+dataLen > len(payload) {
			return 0, nil, fmt.Errorf("truncated at block %d data", i)
		}
		blocks[i].Data = payload[pos : pos+dataLen]
		pos += dataLen
	}

	return totalSize, blocks, nil
}

// applyDelta escreve cada bloco alterado em seu offset no arquivo de destino e trunca o arquivo
// para totalSize (trata arquivos que encolheram).
func applyDelta(path string, totalSize int64, blocks []DeltaBlock) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// O_RDWR é necessário para seek + escrita em offset aleatório (aplicar patch em offsets
	// específicos). O_WRONLY só permite escritas sequenciais. O_CREATE cria o arquivo caso ele
	// ainda não exista (primeiro delta recebido antes de um opWrite completo ter chegado).
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	for _, b := range blocks {
		if _, err := f.Seek(b.Offset, io.SeekStart); err != nil {
			return fmt.Errorf("seek to offset %d: %w", b.Offset, err)
		}
		if _, err := f.Write(b.Data); err != nil {
			return fmt.Errorf("write block at offset %d: %w", b.Offset, err)
		}
	}

	// O Truncate garante a corretude quando o arquivo encolheu.
	if err := f.Truncate(totalSize); err != nil {
		return fmt.Errorf("truncate to %d: %w", totalSize, err)
	}

	return nil
}

// ── Protocolo de comunicação ───────────────────────────────────────────────────

func writeMessage(w io.Writer, op byte, path string, payload []byte) error {
	pathBytes := []byte(path)
	buf := make([]byte, 1+4+8+len(pathBytes)+len(payload))
	buf[0] = op
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(pathBytes)))
	binary.BigEndian.PutUint64(buf[5:13], uint64(len(payload)))
	copy(buf[13:], pathBytes)
	copy(buf[13+len(pathBytes):], payload)
	_, err := w.Write(buf)
	return err
}

func readMessage(r io.Reader) (op byte, path string, payload []byte, err error) {
	var header [13]byte
	if _, err = io.ReadFull(r, header[:]); err != nil {
		return
	}

	op = header[0]
	pathLen := binary.BigEndian.Uint32(header[1:5])
	payloadLen := binary.BigEndian.Uint64(header[5:13])

	pathBuf := make([]byte, pathLen)
	if _, err = io.ReadFull(r, pathBuf); err != nil {
		err = fmt.Errorf("read path: %w", err)
		return
	}
	path = string(pathBuf)

	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err = io.ReadFull(r, payload); err != nil {
			err = fmt.Errorf("read payload: %w", err)
			return
		}
	}

	return
}

// ── Lista de exclusão ──────────────────────────────────────────────────────────

func (m *Manager) exclude(path string) {
	m.excludeMu.Lock()
	m.excluded[path] = time.Now().Add(excludeTTL)
	m.excludeMu.Unlock()
}

func (m *Manager) isExcluded(path string) bool {
	m.excludeMu.RLock()
	exp, ok := m.excluded[path]
	m.excludeMu.RUnlock()

	if !ok {
		return false
	}
	if time.Now().After(exp) {
		m.excludeMu.Lock()
		delete(m.excluded, path)
		m.excludeMu.Unlock()
		return false
	}
	return true
}

// ── Auxiliares ─────────────────────────────────────────────────────────────────

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// ── LWW (Last-Write-Wins determinístico) ─────────────────────────────────────

// wrapLWW prefixa o cabeçalho LWW de 40 bytes (versão + sha256) a um corpo.
func wrapLWW(version int64, hash [32]byte, body []byte) []byte {
	buf := make([]byte, lwwHeaderSize+len(body))
	binary.BigEndian.PutUint64(buf[0:8], uint64(version))
	copy(buf[8:lwwHeaderSize], hash[:])
	copy(buf[lwwHeaderSize:], body)
	return buf
}

// unwrapLWW analisa o cabeçalho LWW de um payload e retorna versão, hash e corpo.
func unwrapLWW(payload []byte) (version int64, hash [32]byte, body []byte, err error) {
	if len(payload) < lwwHeaderSize {
		err = fmt.Errorf("payload too short for LWW header: %d < %d", len(payload), lwwHeaderSize)
		return
	}
	version = int64(binary.BigEndian.Uint64(payload[0:8]))
	copy(hash[:], payload[8:lwwHeaderSize])
	body = payload[lwwHeaderSize:]
	return
}

// shouldApply decide se aplica uma escrita remota com base no mtime do arquivo, com desempate por
// sha256. Retorna (apply, reason). O reason é uma etiqueta curta adequada para log e filtragem no
// explog (no_local, newer_remote, older_remote, tiebreak_remote, tiebreak_local, identical,
// stat_error, read_local_error).
func shouldApply(absPath string, remoteVersion int64, remoteHash [32]byte) (bool, string) {
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return true, "no_local"
		}
		return false, "stat_error"
	}
	localVersion := info.ModTime().UnixNano()
	if remoteVersion > localVersion {
		return true, "newer_remote"
	}
	if remoteVersion < localVersion {
		return false, "older_remote"
	}
	// Desempate: compara o sha256 do conteúdo local com o hash remoto.
	localContent, err := os.ReadFile(absPath)
	if err != nil {
		return false, "read_local_error"
	}
	localHash := sha256.Sum256(localContent)
	cmp := bytes.Compare(remoteHash[:], localHash[:])
	if cmp > 0 {
		return true, "tiebreak_remote"
	}
	if cmp == 0 {
		return false, "identical"
	}
	return false, "tiebreak_local"
}

// writeFileLWW escreve o conteúdo e então força o mtime do arquivo para `version`, de modo que
// qualquer nó que receba este arquivo depois compare contra o instante do criador original em vez
// do horário de escrita local do receptor.
func writeFileLWW(path string, content []byte, version int64) error {
	if err := writeFile(path, content); err != nil {
		return err
	}
	t := time.Unix(0, version)
	return os.Chtimes(path, t, t)
}

// applyDeltaLWW aplica o delta e então força o mtime do arquivo para `version`.
func applyDeltaLWW(path string, totalSize int64, blocks []DeltaBlock, version int64) error {
	if err := applyDelta(path, totalSize, blocks); err != nil {
		return err
	}
	t := time.Unix(0, version)
	return os.Chtimes(path, t, t)
}

// removeFileLWW remove o arquivo. Retorna nil se o arquivo não existia (idempotente).
func removeFileLWW(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
