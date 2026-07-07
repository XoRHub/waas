package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xorhub/waas/api-server/internal/config"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/handler"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/api-server/internal/server"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/operator/pkg/naming"
	"github.com/xorhub/waas/shared/auth"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("api-server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	slog.Info("database ready", "dialect", db.Dialect)

	signer, err := loadSigner(cfg)
	if err != nil {
		return err
	}

	kube, err := k8s.NewClient(cfg.DevMode)
	if err != nil {
		return err
	}

	// Same startup gate as the operator: an invalid global placement
	// pattern refuses to start rather than silently falling back — the
	// two deployments share one Helm values key and must agree.
	if cfg.DefaultNamespacePattern != "" {
		if err := naming.ValidatePattern(cfg.DefaultNamespacePattern); err != nil {
			return fmt.Errorf("invalid WAAS_DEFAULT_NAMESPACE_PATTERN %q: %w", cfg.DefaultNamespacePattern, err)
		}
	}

	users := repository.NewSQLUserRepository(db)
	sessions := repository.NewSQLSessionRepository(db)
	remotes := repository.NewSQLRemoteWorkspaceRepository(db)
	auditRepo := repository.NewSQLAuditRepository(db)

	audit := service.NewAuditService(auditRepo)
	authSvc := service.NewAuthService(users, signer, audit, cfg.JWTIssuer, cfg.AccessTokenTTL)
	var oidcSvc *service.OIDCService
	if cfg.OIDC.Enabled() {
		oidcSvc = service.NewOIDCService(cfg.OIDC, users, audit, signer, cfg.JWTIssuer, cfg.AccessTokenTTL)
		slog.Info("OIDC login enabled", "issuer", cfg.OIDC.IssuerURL, "provider", cfg.OIDC.ProviderName)
	}
	userSvc := service.NewUserService(users, audit)
	templateSvc := service.NewTemplateService(kube, cfg.WorkspaceNamespace, audit)
	workspaceSvc := service.NewWorkspaceService(kube, cfg.WorkspaceNamespace, users, sessions, audit, signer,
		cfg.JWTIssuer, cfg.ConnectionTokenTTL).
		WithRemoteWorkspaces(remotes).
		WithDefaultNamespacePattern(cfg.DefaultNamespacePattern)
	remoteSvc := service.NewRemoteWorkspaceService(kube, cfg.WorkspaceNamespace, users, remotes, sessions,
		audit, signer, cfg.JWTIssuer, cfg.ConnectionTokenTTL)
	if relay := service.NewHTTPWoLRelay(cfg.WoL.RelayURL, cfg.WoL.AuthToken); relay != nil {
		remoteSvc = remoteSvc.WithWoL(relay)
		slog.Info("Wake-on-LAN relay enabled", "url", cfg.WoL.RelayURL)
	}
	// SSE change notifications: one shared Kubernetes watch relays every
	// Workspace change (portal, kubectl, operator status, cron edges);
	// remote-workspace mutations notify directly (DB-backed, single writer).
	events := service.NewEventHub()
	remoteSvc = remoteSvc.WithEvents(events)
	sessionSvc := service.NewSessionService(sessions)
	governanceSvc := service.NewGovernanceService(kube, cfg.WorkspaceNamespace, users, audit)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := userSvc.EnsureBootstrapAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return err
	}

	// Idle enforcement lives here (not in the operator) because only the
	// api-server knows about desktop sessions.
	go service.NewIdleSweeper(kube, cfg.WorkspaceNamespace, sessions, audit, cfg.IdleSweepInterval).Run(ctx)
	// Same data-ownership rule for the session sweeper: it closes session
	// rows whose workspace/remote was deleted outside this API (kubectl,
	// ArgoCD prune) or whose end-of-session callback was lost.
	go service.NewSessionSweeper(kube, cfg.WorkspaceNamespace, sessions, remotes, audit, cfg.SessionSweepInterval).Run(ctx)
	go events.RunWorkspaceWatch(ctx, kube, cfg.WorkspaceNamespace)

	router := server.New(cfg, signer, server.Handlers{
		Auth:             handler.NewAuthHandler(authSvc, oidcSvc, cfg.OIDC, signer),
		Users:            handler.NewUserHandler(userSvc),
		Templates:        handler.NewTemplateHandler(templateSvc),
		Workspaces:       handler.NewWorkspaceHandler(workspaceSvc),
		RemoteWorkspaces: handler.NewRemoteWorkspaceHandler(remoteSvc),
		Admin:            handler.NewAdminHandler(audit, sessionSvc),
		Internal:         handler.NewInternalHandler(workspaceSvc),
		Governance:       handler.NewGovernanceHandler(governanceSvc),
		Meta:             handler.NewMetaHandler(),
		Events:           handler.NewEventsHandler(events),
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api-server listening", "addr", cfg.ListenAddr, "tls", cfg.TLSCertFile != "")
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			errCh <- srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// loadSigner reads the RS256 signing key from the mounted Secret, or
// generates an ephemeral one in dev mode.
func loadSigner(cfg *config.Config) (*auth.Signer, error) {
	if cfg.JWTPrivateKeyPath != "" {
		pemBytes, err := os.ReadFile(cfg.JWTPrivateKeyPath)
		if err != nil {
			return nil, err
		}
		return auth.ParseSignerPEM(pemBytes)
	}
	if !cfg.DevMode {
		return nil, errors.New("WAAS_JWT_PRIVATE_KEY_FILE is required outside dev mode")
	}
	slog.Warn("dev mode: using an ephemeral JWT signing key")
	return auth.GenerateSigner()
}
