package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ccallazans/syncli/internal/discovery"
	"github.com/ccallazans/syncli/internal/explog"
	"github.com/ccallazans/syncli/internal/transfer"
	"github.com/ccallazans/syncli/internal/watcher"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "peers":
			runPeers(os.Args[2:])
			return
		case "help", "--help", "-h":
			printUsage()
			return
		}
	}

	runSync(os.Args[1:])
}

// ── "syncli peers" ───────────────────────────────────────────────────────────

func runPeers(args []string) {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	timeout := fs.Duration("timeout", 5*time.Second, "How long to scan for peers")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `
Usage: syncli peers [--timeout <duration>]

Scans the local network for syncli peers and shows which sync groups they share.

Options:
  --timeout <duration>  Scan duration (default: 5s)

`)
	}
	fs.Parse(args)

	fmt.Fprintf(os.Stdout, "Scanning for syncli peers on the local network (%.0fs)...\n\n",
		timeout.Seconds())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peers, err := discovery.Scan(ctx, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(peers) == 0 {
		fmt.Println("No peers found.")
		fmt.Println("Make sure other syncli nodes are running on the same network.")
		return
	}

	// Agrupa os peers por nodeID para que múltiplos grupos do mesmo nó apareçam juntos.
	type nodeInfo struct {
		id     string
		addr   string
		groups []string
	}
	byNode := make(map[string]*nodeInfo)
	order := []string{}
	for _, p := range peers {
		if _, ok := byNode[p.ID]; !ok {
			byNode[p.ID] = &nodeInfo{id: p.ID, addr: p.Addr}
			order = append(order, p.ID)
		}
		byNode[p.ID].groups = append(byNode[p.ID].groups, p.Group)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "  NODE ID\tADDRESS\tGROUPS")
	fmt.Fprintln(w, "  -------\t-------\t------")
	for _, id := range order {
		n := byNode[id]
		fmt.Fprintf(w, "  %s\t%s\t%s\n", n.id, n.addr, strings.Join(n.groups, ", "))
	}
	w.Flush()

	fmt.Printf("\nFound %d peer(s).\n", len(byNode))
	fmt.Println("\nTip: join a group with  syncli --group <name> --dir <path>")
}

// ── "syncli" (daemon de sincronização) ───────────────────────────────────────

// peerFlags permite que --peer seja informado várias vezes na linha de comando.
type peerFlags []string

func (p *peerFlags) String() string     { return fmt.Sprintf("%v", *p) }
func (p *peerFlags) Set(v string) error { *p = append(*p, v); return nil }

func runSync(args []string) {
	fs := flag.NewFlagSet("syncli", flag.ExitOnError)
	group    := fs.String("group", "", "Sync group name (required)")
	dir      := fs.String("dir", "", "Local directory to sync (required)")
	port     := fs.Int("port", 9001, "TCP port to listen on")
	autoJoin := fs.Bool("auto-join", false, "Skip confirmation prompt and join existing peers automatically")
	verbose  := fs.Bool("verbose", false, "Enable verbose/debug logging")
	var manualPeers peerFlags
	fs.Var(&manualPeers, "peer", "Manually connect to a peer (host:port) — can be repeated")
	fs.Usage = printUsage
	fs.Parse(args)

	if *group == "" || *dir == "" {
		fmt.Fprintln(os.Stderr, "error: --group and --dir are required")
		printUsage()
		os.Exit(1)
	}

	setupLogging(*verbose)
	explog.Init()

	if err := os.MkdirAll(*dir, 0755); err != nil {
		slog.Error("cannot create sync directory", "dir", *dir, "err", err)
		os.Exit(1)
	}

	nodeID := buildNodeID()

	// ── Passo 1: procura por peers já existentes neste grupo ──────────────────
	// O tempo de 3s acompanha o browseTime da descoberta, dando aos peers mDNS tempo
	// suficiente para responder antes de o nó decidir se é o primeiro do grupo. Manter
	// os dois valores iguais torna a relação entre eles fácil de entender.
	slog.Info("peer scan started", "group", *group)
	fmt.Printf("Scanning for peers in group %q... (3s)\n\n", *group)

	scanCtx, scanCancel := context.WithCancel(context.Background())
	existingPeers, err := discovery.ScanGroup(scanCtx, 3*time.Second, *group)
	scanCancel()
	if err != nil {
		slog.Error("scan failed", "err", err)
		os.Exit(1)
	}

	// ── Passo 2: confirmação de entrada no grupo ──────────────────────────────
	if len(existingPeers) > 0 {
		fmt.Printf("Found %d peer(s) already syncing group %q:\n", len(existingPeers), *group)
		for _, p := range existingPeers {
			fmt.Printf("  • %s  (%s)\n", p.ID, p.Addr)
		}
		fmt.Println()

		if !*autoJoin && !confirmJoin(*group) {
			fmt.Println("Aborted.")
			return
		}
		if *autoJoin {
			fmt.Printf("Auto-joining %d peer(s) in group %q.\n\n", len(existingPeers), *group)
		}
	} else {
		fmt.Printf("No existing peers found for group %q. Starting as first node.\n\n", *group)
	}

	// ── Passo 3: inicialização do daemon ──────────────────────────────────────
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("syncli starting", "nodeID", nodeID, "group", *group, "dir", *dir, "port", *port)
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// signal.NotifyContext propaga SIGTERM/SIGINT para todos os subsistemas via cancelamento
	// de contexto, permitindo um encerramento ordenado e gracioso: o watcher fecha seu canal →
	// o loop de envio termina → o servidor TCP fecha.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Watcher
	slog.Info("[1/3] starting file watcher...")
	w, err := watcher.New(*dir)
	if err != nil {
		slog.Error("watcher init failed", "err", err)
		os.Exit(1)
	}
	if err := w.Start(ctx); err != nil {
		slog.Error("watcher start failed", "err", err)
		os.Exit(1)
	}
	slog.Info("file watcher active", "dir", *dir)

	// 2. Descoberta (discovery)
	slog.Info("[2/3] starting mDNS discovery...")
	disc := discovery.New(nodeID, *group, *port)

	// Pré-carrega os peers descobertos durante o scan inicial para que fiquem disponíveis ao
	// motor de transferência imediatamente. Sem isto, haveria uma janela de 5s (primeiro ciclo
	// de browse) na qual eventos de arquivo seriam descartados silenciosamente por falta de peers.
	for _, p := range existingPeers {
		disc.AddSeedPeer(p)
	}

	if err := disc.Start(ctx); err != nil {
		slog.Error("discovery start failed", "err", err)
		os.Exit(1)
	}
	slog.Info("mDNS discovery active", "group", *group)

	for _, addr := range manualPeers {
		disc.AddManualPeer(addr)
	}
	if len(manualPeers) > 0 {
		slog.Info("manual peers registered", "count", len(manualPeers))
	}

	// 3. Motor de transferência
	slog.Info("[3/3] starting transfer engine...")
	mgr := transfer.New(*dir, *port, disc, w.Events())
	if err := mgr.Start(ctx); err != nil {
		slog.Error("transfer engine start failed", "err", err)
		os.Exit(1)
	}
	slog.Info("transfer engine active — ready to sync")

	// ── 4. Sincronização completa ao entrar no grupo ──────────────────────────
	// Se havia peers ao entrarmos, pede um snapshot completo ao primeiro que responder,
	// de modo que começamos já com uma cópia local completa dos arquivos.
	if len(existingPeers) > 0 {
		slog.Info("requesting full snapshot from group peers...", "candidates", len(existingPeers))
		snapshotOK := false
		for _, p := range existingPeers {
			if err := mgr.RequestSnapshot(p); err != nil {
				slog.Warn("snapshot attempt failed, trying next peer", "peer", p.Addr, "err", err)
				continue
			}
			snapshotOK = true
			break
		}
		if !snapshotOK {
			slog.Warn("could not complete snapshot from any peer — starting with local files only")
		}
	}

	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	slog.Info("syncli is running — press Ctrl+C to stop")
	slog.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	<-ctx.Done()
	slog.Info("signal received, shutting down gracefully...")
	slog.Info("syncli stopped")
}

// confirmJoin pergunta ao usuário e retorna true caso ele queira entrar no grupo.
func confirmJoin(group string) bool {
	fmt.Printf("Join group %q? [y/N]: ", group)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}

// ── Auxiliares ─────────────────────────────────────────────────────────────────

func setupLogging(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))
}

// buildNodeID retorna um identificador único para este processo.
//
// O hostname sozinho não é único quando várias instâncias rodam no mesmo host (por exemplo,
// containers Docker). O PID garante unicidade por processo. O PID muda a cada reinício, o que
// é intencional: o nó se reanuncia à rede com uma nova identidade.
func buildNodeID() string {
	if id := os.Getenv("SYNCLI_NODE_ID"); id != "" {
		return id
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "node"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}

func printUsage() {
	fmt.Fprint(os.Stderr, `
syncli — P2P file synchronization over local network

USAGE:
  syncli --group <name> --dir <path> [options]
  syncli peers [--timeout <duration>]
  syncli help

COMMANDS:
  (default)   Start syncing a directory under a named group
  peers       List all syncli peers and their groups on the local network
  help        Show this help message

SYNC OPTIONS:
  --group <name>       Sync group name (required)
  --dir <path>         Local directory to sync (required)
  --port <port>        TCP port to listen on (default: 9001)
  --peer <host:port>   Manually connect to a peer, can be repeated
  --auto-join          Skip confirmation prompt, connect to existing peers automatically
  --verbose            Enable verbose/debug logging

PEERS OPTIONS:
  --timeout <duration>  Scan duration (default: 5s)

EXAMPLES:
  # Start sharing a directory — first node creates the group
  syncli --group photos --dir ~/Photos

  # Join an existing group (will scan and ask for confirmation)
  syncli --group photos --dir ~/local-photos

  # Same but skip the confirmation prompt (useful for scripts/Docker)
  syncli --group photos --dir ~/local-photos --auto-join

  # Run two sync groups on the same machine (use different ports)
  syncli --group photos    --dir ~/Photos    --port 9001
  syncli --group documents --dir ~/Documents --port 9002

  # See all peers and their groups on the network
  syncli peers
  syncli peers --timeout 10s

  # Manually add a peer when mDNS is unavailable
  syncli --group photos --dir ~/Photos --peer 192.168.1.10:9001

NOTES:
  • Peers in the same group are discovered automatically via mDNS.
  • A node only connects to peers in the same group.
  • Manual peers (--peer) are persistent and skip mDNS timeout removal.
  • Conflict resolution policy: last-write-wins.
  • Communication is unencrypted — intended for trusted local networks only.

`)
}
