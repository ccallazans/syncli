package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ccallazans/syncli/internal/explog"
	"github.com/fsnotify/fsnotify"
)

// OpType representa o tipo de evento de arquivo.
type OpType uint8

// Os valores são idênticos aos opcodes da transferência (opWrite=0x01, opRemove=0x02) para que
// um OpType possa ser convertido diretamente para o byte do protocolo sem tabela de conversão.
const (
	OpWrite  OpType = 0x01
	OpRemove OpType = 0x02
)

func (o OpType) String() string {
	switch o {
	case OpWrite:
		return "write"
	case OpRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Event é um evento de mudança no sistema de arquivos com caminho relativo.
type Event struct {
	Op   OpType
	Path string // relativo ao diretório monitorado
}

// Watcher monitora um diretório recursivamente e emite eventos com debounce.
type Watcher struct {
	dir    string
	fsw    *fsnotify.Watcher
	events chan Event
}

// New cria um novo Watcher para o diretório informado.
func New(dir string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watcher: create fsnotify: %w", err)
	}
	return &Watcher{
		dir: dir,
		fsw: fsw,
		// O buffer de 64 absorve rajadas de "salvar tudo" dos editores sem bloquear o loop
		// interno de eventos do fsnotify. Eventos bloqueados aqui fariam o fsnotify descartar
		// eventos do kernel.
		events: make(chan Event, 64),
	}, nil
}

// Events retorna o canal somente leitura de eventos de arquivo.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Start começa a monitorar o diretório. Bloqueia até o ctx ser cancelado.
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.addDirRecursive(w.dir); err != nil {
		w.fsw.Close()
		return err
	}
	go w.run(ctx)
	return nil
}

// addDirRecursive registra todos os subdiretórios no fsnotify.
//
// O fsnotify não tem monitoramento recursivo nativo; percorremos a árvore de diretórios na
// inicialização e registramos cada subdiretório. Novos subdiretórios criados em tempo de
// execução são registrados quando seu evento Create chega (ver run).
func (w *Watcher) addDirRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("watcher: walk %s: %w", path, err)
		}
		if d.IsDir() {
			if err := w.fsw.Add(path); err != nil {
				return fmt.Errorf("watcher: watch dir %s: %w", path, err)
			}
			slog.Debug("watching directory", "path", path)
		}
		return nil
	})
}

func (w *Watcher) run(ctx context.Context) {
	defer func() {
		w.fsw.Close()
		close(w.events)
	}()

	// debounce: caminho -> timer pendente
	pending := make(map[string]*time.Timer)

	for {
		select {
		case <-ctx.Done():
			return
		case fsEv, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			var op OpType
			switch {
			// Chmod é tratado como escrita porque VS Code e Vim o emitem durante um salvamento
			// atômico (renomeia arquivo temporário + chmod no original). Ignorá-lo perderia
			// mudanças de conteúdo.
			case fsEv.Has(fsnotify.Create) || fsEv.Has(fsnotify.Write) || fsEv.Has(fsnotify.Chmod):
				op = OpWrite
			// Rename é mapeado para OpRemove porque o caminho de origem deixa de existir após o
			// rename. O novo caminho de destino gera seu próprio evento Create, tratado à parte.
			case fsEv.Has(fsnotify.Remove) || fsEv.Has(fsnotify.Rename):
				op = OpRemove
			default:
				continue
			}

			path := fsEv.Name

			// Diretórios são contêineres, não conteúdo. Registramos novos diretórios internamente
			// para que seus filhos sejam monitorados, mas não emitimos eventos para eles — o
			// receptor cria os diretórios pai automaticamente quando o primeiro arquivo filho chega.
			if op == OpWrite {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					if err := w.addDirRecursive(path); err != nil {
						slog.Error("watcher: register new dir", "path", path, "err", err)
					}
					continue // não emite eventos para os próprios diretórios
				}
			}

			if rel, err := filepath.Rel(w.dir, path); err == nil {
				explog.Emit("fs_event", rel, op.String(), 0)
			}

			// Cancela qualquer timer de debounce pendente para este caminho.
			if t, ok := pending[path]; ok {
				t.Stop()
			}

			// O debounce de 200ms colapsa salvamentos multietapa dos editores (escrita temporária
			// → rename → chmod) em um único evento. Determinado empiricamente com VS Code / Vim em
			// ext4. Os eventos carregam caminhos relativos para que peers com pontos de montagem
			// diferentes os apliquem corretamente.
			opSnap, pathSnap := op, path
			pending[path] = time.AfterFunc(200*time.Millisecond, func() {
				rel, err := filepath.Rel(w.dir, pathSnap)
				if err != nil {
					return
				}
				explog.Emit("debounce_out", rel, opSnap.String(), 0)
				ev := Event{Op: opSnap, Path: rel}
				select {
				case w.events <- ev:
					slog.Debug("file event", "op", opSnap, "path", rel)
				default:
					slog.Warn("watcher: event channel full, dropping event", "path", rel)
				}
			})

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Error("watcher error", "err", err)
		}
	}
}
