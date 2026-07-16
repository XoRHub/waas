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

	corev1 "k8s.io/api/core/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

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

	if cfg.MetricsEnabled {
		slog.Info("Prometheus metrics enabled", "endpoint", "/metrics")
	}

	users := repository.NewSQLUserRepository(db)
	sessions := repository.NewSQLSessionRepository(db)
	remotes := repository.NewSQLRemoteWorkspaceRepository(db)
	auditRepo := repository.NewSQLAuditRepository(db)
	catalogRepo := repository.NewSQLCatalogRepository(db)

	audit := service.NewAuditService(auditRepo)
	authSvc := service.NewAuthService(users, signer, audit, cfg.JWTIssuer, cfg.AccessTokenTTL)
	var oidcSvc *service.OIDCService
	if cfg.OIDC.Enabled() {
		oidcSvc = service.NewOIDCService(cfg.OIDC, users, audit, signer, cfg.JWTIssuer, cfg.AccessTokenTTL)
		slog.Info("OIDC login enabled", "issuer", cfg.OIDC.IssuerURL, "provider", cfg.OIDC.ProviderName)
	}
	if cfg.OIDC.OIDCOnly {
		slog.Info("local login disabled (WAAS_LOGIN_OIDC_ONLY): every account signs in through the IdP")
		// Not fatal — the deployment stays recoverable by redeploying
		// without the flag and using the bootstrap admin — but without an
		// admin-group mapping no SSO account can ever reach the admin role.
		if len(cfg.OIDC.AdminGroups) == 0 {
			slog.Warn("WAAS_LOGIN_OIDC_ONLY is set but WAAS_OIDC_ADMIN_GROUPS is empty — " +
				"no account can become admin via SSO; only the bootstrap admin " +
				"(unreachable while this flag is set) has the admin role")
		}
	}
	userSvc := service.NewUserService(users, audit)
	templateSvc := service.NewTemplateService(kube, cfg.WorkspaceNamespace, audit)
	workspaceSvc := service.NewWorkspaceService(kube, cfg.WorkspaceNamespace, users, sessions, audit, signer,
		cfg.JWTIssuer, cfg.ConnectionTokenTTL).
		WithRemoteWorkspaces(remotes).
		WithDefaultNamespacePattern(cfg.DefaultNamespacePattern)
	if !cfg.DevMode {
		// pods/exec needs a plain client-go clientset (SPDY streaming);
		// dev mode has no cluster and the resize endpoint answers 503.
		podExec, err := k8s.NewPodExec()
		if err != nil {
			return fmt.Errorf("building pod exec client: %w", err)
		}
		workspaceSvc = workspaceSvc.WithPodExecutor(podExec)
	}
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
	governanceSvc := service.NewGovernanceService(kube, cfg.WorkspaceNamespace, users, audit, catalogRepo)

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
	// Catalog sync also lives here, not in the operator: only the
	// api-server has both database and k8s access needed to write
	// catalog_entries (see CatalogSyncWorker doc comment).
	go service.NewCatalogSyncWorker(kube, cfg.WorkspaceNamespace, catalogRepo, cfg.CatalogSyncInterval).Run(ctx)
	go events.RunWorkspaceWatch(ctx, kube, cfg.WorkspaceNamespace)
	// Admin-managed objects change through GitOps and kubectl too: watch
	// them and broadcast their KIND (never data — clients re-fetch through
	// the per-user authorized API). Home volumes live wherever their
	// workspace was placed: cluster-wide watch, scoped to the owner.
	go events.RunWatch(ctx, kube, &waasv1alpha1.WorkspaceTemplateList{}, "templates", nil, k8sclient.InNamespace(cfg.WorkspaceNamespace))
	go events.RunWatch(ctx, kube, &waasv1alpha1.WorkspaceImageList{}, "images", nil, k8sclient.InNamespace(cfg.WorkspaceNamespace))
	go events.RunWatch(ctx, kube, &waasv1alpha1.WorkspacePolicyList{}, "policies", nil, k8sclient.InNamespace(cfg.WorkspaceNamespace))
	go events.RunWatch(ctx, kube, &corev1.PersistentVolumeClaimList{}, "volumes",
		func(obj k8sclient.Object) string { return obj.GetLabels()["waas.xorhub.io/owner"] },
		k8sclient.MatchingLabels{"app.kubernetes.io/managed-by": "waas-operator"})

	router := server.New(cfg, signer, server.Handlers{
		Auth:             handler.NewAuthHandler(authSvc, oidcSvc, cfg.OIDC, signer),
		Users:            handler.NewUserHandler(userSvc),
		Templates:        handler.NewTemplateHandler(templateSvc),
		Workspaces:       handler.NewWorkspaceHandler(workspaceSvc).WithEventsPollInterval(cfg.EventsPollInterval),
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
		// ReadTimeout also arms the background read the server keeps
		// during long responses (client-disconnect detection): hitting it
		// cancels the request context. The SSE handler is the one
		// long-lived response and clears its deadlines explicitly via
		// ResponseController (see handler/events.go).
		ReadTimeout: 30 * time.Second,
		// WriteTimeout stays 0 ON PURPOSE: any non-zero value would cut
		// every SSE stream at the deadline.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
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
