package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
	"herdr-space/internal/api"
	"herdr-space/internal/auth"
	"herdr-space/internal/runtime"
	"herdr-space/internal/store"
	"herdr-space/internal/webassets"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: herdr-space serve|auth setup|auth change|auth recovery|backup")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	defaultDir := filepath.Join(home, ".local/share/herdr-space")
	switch args[0] {
	case "serve":
		f := flag.NewFlagSet("serve", flag.ContinueOnError)
		listen := f.String("listen", "127.0.0.1:9380", "listen address")
		origin := f.String("origin", "", "required browser origin (HTTPS, or loopback HTTP with --allow-insecure-local)")
		dir := f.String("data-dir", defaultDir, "data directory")
		local := f.Bool("allow-insecure-local", false, "allow local HTTP and insecure cookie")
		herdr := f.String("herdr-binary", "", "HERDR binary")
		config := f.String("herdr-config-dir", "", "HERDR config directory")
		roots := f.String("project-roots", home, "comma-separated project roots")
		bootstrapIssuer := f.String("bootstrap-issuer", "", "Cloudflare Access team issuer for first browser setup")
		bootstrapAudience := f.String("bootstrap-audience", "", "Cloudflare Access application audience")
		bootstrapEmail := f.String("bootstrap-email", "", "comma-separated exact owner emails")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		host, _, e := net.SplitHostPort(*listen)
		if e != nil {
			return e
		}
		if *origin == "" {
			return errors.New("--origin is required (HTTPS, or loopback HTTP with --allow-insecure-local)")
		}
		if *local && !loopback(host) {
			return errors.New("insecure mode requires loopback listener")
		}
		manager, e := runtime.New(runtime.Config{HerdrBinary: *herdr, HerdrConfigDir: *config, ProjectRoots: strings.Split(*roots, ",")})
		if e != nil {
			return e
		}
		server, e := api.New(api.Config{DataDir: *dir, Origin: *origin, AllowInsecureLocal: *local, ProjectRoots: strings.Split(*roots, ","), HerdrBinary: *herdr, HerdrConfigDir: *config, BootstrapIssuer: *bootstrapIssuer, BootstrapAudience: *bootstrapAudience, BootstrapEmail: *bootstrapEmail}, webassets.FS(), manager)
		if e != nil {
			_ = manager.Close()
			return e
		}
		if e := manager.SetReferenceStore(context.Background(), server.Store); e != nil {
			_ = manager.Close()
			_ = server.Close()
			return e
		}
		defer func() { _ = manager.Close(); _ = server.Close() }()
		httpServer := &http.Server{Addr: *listen, Handler: server, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
		httpServer.RegisterOnShutdown(server.BeginShutdown)
		listener, e := net.Listen("tcp", *listen)
		if e != nil {
			return e
		}
		manager.Start(context.Background())
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fmt.Fprintf(os.Stderr, "HERDR Space listening on %s\n", *listen)
		return serveHTTP(ctx, httpServer, listener, 10*time.Second)
	case "auth":
		if len(args) < 2 {
			return errors.New("usage: auth setup|change|recovery")
		}
		f := flag.NewFlagSet("auth", flag.ContinueOnError)
		dir := f.String("data-dir", defaultDir, "data directory")
		if e := f.Parse(args[2:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("interactive terminal required")
		}
		st, e := store.Open(filepath.Join(*dir, "space.db"))
		if e != nil {
			return e
		}
		defer st.Close()
		a, e := auth.New(st, filepath.Join(*dir, "auth.key"))
		if e != nil {
			return e
		}
		ctx := context.Background()
		switch args[1] {
		case "setup":
			u, e := prompt("Username: ")
			if e != nil {
				return e
			}
			p, e := password("Password (at least 16 characters): ")
			if e != nil {
				return e
			}
			confirm, e := password("Confirm password: ")
			if e != nil {
				return e
			}
			if p != confirm {
				return errors.New("passwords differ")
			}
			secret := auth.GenerateSecret()
			fmt.Println("Authenticator secret (TOTP):", secret)
			otp, e := prompt("Current six-digit authenticator code: ")
			if e != nil {
				return e
			}
			codes, e := a.Enroll(ctx, u, p, secret, otp)
			if e != nil {
				return e
			}
			showRecovery(codes)
			return nil
		case "change":
			old, e := password("Current password: ")
			if e != nil {
				return e
			}
			otp, e := prompt("Current authenticator or recovery code: ")
			if e != nil {
				return e
			}
			next, e := password("New password (at least 16 characters): ")
			if e != nil {
				return e
			}
			again, e := password("Confirm new password: ")
			if e != nil {
				return e
			}
			if next != again {
				return errors.New("passwords differ")
			}
			secret, codes, e := a.Change(ctx, old, otp, next)
			if e != nil {
				return e
			}
			showEnrollment(secret, codes)
			return nil
		case "recovery":
			p, e := password("Password: ")
			if e != nil {
				return e
			}
			otp, e := prompt("Authenticator or recovery code: ")
			if e != nil {
				return e
			}
			codes, e := a.RegenerateRecovery(ctx, p, otp)
			if e != nil {
				return e
			}
			fmt.Println("Store these one-time recovery codes securely:")
			for _, code := range codes {
				fmt.Println(code)
			}
			return nil
		default:
			return errors.New("unknown auth command")
		}
	case "backup":
		f := flag.NewFlagSet("backup", flag.ContinueOnError)
		dir := f.String("data-dir", defaultDir, "data directory")
		retain := f.Int("retain", 7, "daily snapshots to retain")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 || *retain < 1 || *retain > 365 {
			return errors.New("invalid backup arguments")
		}
		dest, e := runBackup(*dir, *retain)
		if e != nil {
			return e
		}
		fmt.Println(dest)
		return nil
	default:
		return errors.New("unknown command")
	}
}
func loopback(h string) bool {
	ip := net.ParseIP(h)
	return h == "localhost" || ip != nil && ip.IsLoopback()
}
func prompt(label string) (string, error) {
	fmt.Print(label)
	v, e := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(v), e
}
func password(label string) (string, error) {
	fmt.Print(label)
	b, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(b), e
}
func showEnrollment(secret string, codes []string) {
	fmt.Println("Authenticator secret (TOTP):", secret)
	showRecovery(codes)
}
func showRecovery(codes []string) {
	fmt.Println("Store these one-time recovery codes securely:")
	for _, code := range codes {
		fmt.Println(code)
	}
}
