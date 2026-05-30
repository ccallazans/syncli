package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/ccallazans/syncli/internal/explog"
	"github.com/grandcat/zeroconf"
)

const (
	// Tipo de serviço DNS-SD (RFC 6763). O underscore inicial marca um tipo de serviço e
	// "_tcp" declara o transporte TCP. Avahi e Bonjour reconhecem essa convenção
	// automaticamente — não é necessário um daemon de descoberta próprio.
	mdnsService = "_syncli._tcp"
	mdnsDomain  = "local."

	// Janela de coleta por ciclo de consulta PTR. O mDNS usa multicast UDP
	// (224.0.0.251:5353); a maioria dos peers responde em menos de 1s, mas 3s cobre hosts
	// sobrecarregados. Espelha o scan inicial de 3s em main.go.
	browseTime = 3 * time.Second

	// Intervalo entre ciclos de descoberta. Equilibra capacidade de resposta contra tráfego
	// multicast. Valores abaixo de 2s arriscam tempestades de pacotes em redes com muitos nós.
	browseEvery = 5 * time.Second

	// Um peer só é removido quando missed > maxMissed (estritamente maior), absorvendo
	// cerca de 20s de falhas transitórias (4 × browseEvery). Três ciclos perdidos
	// consecutivos ainda são tolerados antes da remoção.
	maxMissed = 3
)

// Peer representa um nó descoberto ou configurado manualmente.
type Peer struct {
	ID    string
	Addr  string // host:port
	Group string // nome do grupo de sincronização
}

type peerEntry struct {
	peer   Peer
	missed int
	// isManual marca peers registrados via --peer. Peers manuais nunca são removidos pelo
	// contador de falhas — permanecem na lista ativa independentemente da visibilidade mDNS.
	isManual bool
}

// Discovery gerencia o registro do serviço mDNS e a descoberta de peers de um grupo.
type Discovery struct {
	nodeID string
	group  string
	port   int

	// O RWMutex permite leituras concorrentes pelas goroutines de envio do módulo de
	// transferência sem serializar contra as escritas periódicas da goroutine de descoberta.
	mu    sync.RWMutex
	peers map[string]*peerEntry // indexado pelo ID do peer
}

// New cria uma instância de Discovery para o nó e grupo de sincronização informados.
func New(nodeID, group string, port int) *Discovery {
	return &Discovery{
		nodeID: nodeID,
		group:  group,
		port:   port,
		peers:  make(map[string]*peerEntry),
	}
}

// Start registra o serviço mDNS e inicia a descoberta de peers.
func (d *Discovery) Start(ctx context.Context) error {
	// Os registros TXT (RFC 6763) carregam metadados por serviço como pares chave=valor.
	// "id" identifica o nó; "group" permite que os peers filtrem nós irrelevantes sem abrir
	// uma conexão TCP. Ambos os campos são lidos em entryToPeer.
	txt := []string{
		"id=" + d.nodeID,
		"group=" + d.group,
	}
	// Embutir nodeID+group no nome da instância permite que um único host rode várias
	// instâncias do Syncli (grupos diferentes em portas diferentes) sem colisão de nomes DNS-SD.
	server, err := zeroconf.Register(d.nodeID+"."+d.group, mdnsService, mdnsDomain, d.port, txt, nil)
	if err != nil {
		return fmt.Errorf("discovery: register mDNS service: %w", err)
	}
	slog.Info("mDNS service registered", "nodeID", d.nodeID, "group", d.group, "port", d.port)

	go func() {
		<-ctx.Done()
		server.Shutdown()
		slog.Debug("mDNS service unregistered")
	}()

	go d.browseLoop(ctx)
	return nil
}

// AddManualPeer registra um peer que é sempre mantido, independentemente dos ciclos mDNS.
func (d *Discovery) AddManualPeer(addr string) {
	id := "manual:" + addr
	d.mu.Lock()
	d.peers[id] = &peerEntry{
		peer:     Peer{ID: id, Addr: addr, Group: d.group},
		isManual: true,
	}
	d.mu.Unlock()
	slog.Info("manual peer added", "addr", addr, "group", d.group)
}

// AddSeedPeer pré-carrega um peer descoberto antes do daemon iniciar (ex.: do scan de entrada).
func (d *Discovery) AddSeedPeer(p Peer) {
	d.mu.Lock()
	d.peers[p.ID] = &peerEntry{peer: p}
	d.mu.Unlock()
	slog.Info("peer discovered", "nodeID", p.ID, "addr", p.Addr, "group", p.Group)
	explog.EmitFields("peer_discovered", map[string]any{
		"peer_id": p.ID,
		"addr":    p.Addr,
		"group":   p.Group,
		"source":  "seed",
	})
}

// GetPeers retorna um snapshot da lista atual de peers do mesmo grupo.
func (d *Discovery) GetPeers() []Peer {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peers := make([]Peer, 0, len(d.peers))
	for _, e := range d.peers {
		peers = append(peers, e.peer)
	}
	return peers
}

func (d *Discovery) browseLoop(ctx context.Context) {
	d.browse(ctx)

	ticker := time.NewTicker(browseEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.browse(ctx)
		}
	}
}

func (d *Discovery) browse(ctx context.Context) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		slog.Error("discovery: create resolver", "err", err)
		return
	}

	entries := make(chan *zeroconf.ServiceEntry)
	browseCtx, cancel := context.WithTimeout(ctx, browseTime)
	defer cancel()

	discovered := make(map[string]Peer)
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for entry := range entries {
			peer, ok := entryToPeer(entry, d.nodeID)
			if !ok || peer.Group != d.group {
				continue // ignora a si mesmo e grupos diferentes
			}
			mu.Lock()
			discovered[peer.ID] = peer
			mu.Unlock()
		}
	}()

	if err := resolver.Browse(browseCtx, mdnsService, mdnsDomain, entries); err != nil {
		slog.Error("discovery: browse", "err", err)
		return
	}

	<-browseCtx.Done()
	wg.Wait()

	d.reconcile(discovered)
}

// reconcile atualiza o mapa de peers com base nos resultados do último browse.
func (d *Discovery) reconcile(discovered map[string]Peer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for id, e := range d.peers {
		if !e.isManual {
			d.peers[id].missed++
		}
	}

	for id, peer := range discovered {
		if existing, ok := d.peers[id]; ok {
			existing.missed = 0
			existing.peer = peer
		} else {
			slog.Info("peer discovered", "nodeID", id, "addr", peer.Addr, "group", peer.Group)
			explog.EmitFields("peer_discovered", map[string]any{
				"peer_id": id,
				"addr":    peer.Addr,
				"group":   peer.Group,
			})
			d.peers[id] = &peerEntry{peer: peer}
		}
	}

	for id, e := range d.peers {
		if !e.isManual && e.missed > maxMissed {
			slog.Info("peer removed (unresponsive)", "nodeID", id, "addr", e.peer.Addr)
			explog.EmitFields("peer_removed", map[string]any{
				"peer_id": id,
				"addr":    e.peer.Addr,
				"missed":  e.missed,
			})
			delete(d.peers, id)
		}
	}
}

// ── Scan avulso (sem registro) ───────────────────────────────────────────────

// Scan faz um browse mDNS único e retorna todos os peers descobertos em todos os grupos.
func Scan(ctx context.Context, timeout time.Duration) ([]Peer, error) {
	return scanFilter(ctx, timeout, "")
}

// ScanGroup faz um browse mDNS único limitado a um grupo de sincronização específico.
func ScanGroup(ctx context.Context, timeout time.Duration, group string) ([]Peer, error) {
	return scanFilter(ctx, timeout, group)
}

func scanFilter(ctx context.Context, timeout time.Duration, group string) ([]Peer, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: create resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry)
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		mu      sync.Mutex
		results []Peer
		wg      sync.WaitGroup
	)
	wg.Add(1)

	go func() {
		defer wg.Done()
		for entry := range entries {
			peer, ok := entryToPeer(entry, "")
			if !ok {
				continue
			}
			if group != "" && peer.Group != group {
				continue
			}
			mu.Lock()
			results = append(results, peer)
			mu.Unlock()
		}
	}()

	if err := resolver.Browse(scanCtx, mdnsService, mdnsDomain, entries); err != nil {
		return nil, fmt.Errorf("discovery: browse: %w", err)
	}

	<-scanCtx.Done()
	wg.Wait()

	return results, nil
}

// ── Auxiliares ─────────────────────────────────────────────────────────────────

// entryToPeer converte uma entrada do zeroconf em um Peer.
// Retorna false se a entrada deve ser ignorada (a si mesmo ou inválida).
func entryToPeer(entry *zeroconf.ServiceEntry, selfID string) (Peer, bool) {
	// O mDNS pode retornar entradas com apenas endereços IPv6. O Syncli opera exclusivamente
	// sobre IPv4 (redes locais domésticas/corporativas típicas), então entradas só com IPv6
	// são ignoradas.
	if len(entry.AddrIPv4) == 0 {
		return Peer{}, false
	}

	var nodeID, group string
	for _, txt := range entry.Text {
		switch {
		case len(txt) > 3 && txt[:3] == "id=":
			nodeID = txt[3:]
		case len(txt) > 6 && txt[:6] == "group=":
			group = txt[6:]
		}
	}

	if nodeID == "" || nodeID == selfID {
		return Peer{}, false
	}

	addr := net.JoinHostPort(entry.AddrIPv4[0].String(), fmt.Sprintf("%d", entry.Port))
	return Peer{ID: nodeID, Addr: addr, Group: group}, true
}
