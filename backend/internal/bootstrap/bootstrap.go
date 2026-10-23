package bootstrap

import (
	"agent-platform/backend/internal/agent/agents/general"
	"agent-platform/backend/internal/agent/agents/travel"
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/agent/memory"
	chatapp "agent-platform/backend/internal/application/chat"
	"agent-platform/backend/internal/client/hotel"
	"agent-platform/backend/internal/client/llm"
	ragmcp "agent-platform/backend/internal/client/mcp"
	"agent-platform/backend/internal/client/train"
	"agent-platform/backend/internal/client/weather"
	prefsvc "agent-platform/backend/internal/service/travelpreference"
	tripsvc "agent-platform/backend/internal/service/trip"
	"context"
	"errors"
	"github.com/cloudwego/eino/adk"
	"net/http"
	"time"

	"agent-platform/backend/internal/config"
	"agent-platform/backend/internal/repository/postgres"
	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	Handler http.Handler
	close   func()
}

func (a *App) Close() { a.close() }
func New(ctx context.Context, cfg config.Config) (*App, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseDSN)
	if err != nil {
		return nil, errors.New("invalid DB_DSN")
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("database unavailable")
	}
	lock, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, errors.New("database lock connection unavailable")
	}
	var held bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(88153002)").Scan(&held); err != nil || !held {
		lock.Release()
		pool.Close()
		return nil, errors.New("another platform instance is active")
	}
	closeApp := func() {
		lock.Exec(context.Background(), "SELECT pg_advisory_unlock(88153002)")
		lock.Release()
		pool.Close()
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		closeApp()
		return nil, errors.New("database migration failed")
	}
	repo := postgres.New(pool)
	settings := settingssvc.New(repo)
	conversations := convsvc.New(repo)
	registry := manager.NewRegistry()
	var rag *ragmcp.Client
	var retriever tripsvc.PolicyRetriever
	if cfg.RAGMCPCommand != "" && cfg.RAGMCPDir != "" && cfg.RAGCollection != "" {
		rag, _ = ragmcp.New(ragmcp.StdioConfig{Command: cfg.RAGMCPCommand, Directory: cfg.RAGMCPDir, Args: cfg.RAGMCPArgs, Collection: cfg.RAGCollection})
		if rag != nil {
			retriever = rag
		}
	}
	providers := tripsvc.Providers{"weather": weather.New(nil, time.Now)}
	if cfg.TrainAPIKey != "" {
		providers["train"] = train.New(cfg.TrainAPIKey, nil, time.Now)
	}
	if cfg.AMAPAPIKey != "" {
		providers["hotel"] = hotel.New(cfg.AMAPAPIKey, nil, time.Now)
	}
	trips := tripsvc.NewWithQueries(repo, providers, time.Now)
	preferences := prefsvc.New(repo)
	if modelClient, err := llm.New(ctx, cfg.ModelBaseURL, cfg.ModelID, cfg.ModelAPIKey); err == nil {
		var a *general.Agent
		a = general.NewAgent(modelClient, settings, func(in contracts.RunInput) adk.CheckPointStore {
			return &general.RepositoryStore{Store: repo, Input: in, Fingerprint: a.Fingerprint()}
		}, cfg.ModelBaseURL+"/"+cfg.ModelID, time.Duration(cfg.PauseSeconds)*time.Second)
		registry.Register("general", a)
		tripAgent, err := travel.NewAgent(travel.Config{Model: modelClient, Trips: trips, Preferences: preferences, Policy: tripsvc.NewPolicyService(retriever), Decisions: repo, Checkpoints: repo, Context: memory.NewTravelContext(preferences, repo, 32000), Identity: cfg.ModelBaseURL + "/" + cfg.ModelID})
		if err == nil {
			registry.Register("travel", tripAgent)
		}
	}
	m := manager.New(repo, memory.New(repo, 32000), registry, contracts.Limits{Iterations: cfg.MaxIterations, ExecutionTimeout: time.Duration(cfg.RunSeconds) * time.Second, ToolTimeout: time.Duration(cfg.ToolSeconds) * time.Second, PauseTimeout: time.Duration(cfg.PauseSeconds) * time.Second})
	if err = m.ReconcileInterrupted(ctx); err != nil {
		m.Close()
		closeApp()
		return nil, errors.New("run recovery reconciliation failed")
	}
	pages := manager.NewLeaseManager(repo, m, time.Now, time.Duration(cfg.LeaseSeconds)*time.Second, time.Duration(cfg.HeartbeatSeconds)*time.Second)
	leaseCtx, stopLeases := context.WithCancel(context.Background())
	leaseDone := make(chan struct{})
	go func() { defer close(leaseDone); pages.Run(leaseCtx) }()
	h := router.New(router.Dependencies{Manager: m, Pages: pages, Chat: chatapp.New(conversations, m), Auth: authsvc.New(repo), Users: usersvc.New(repo), Conversations: conversations, Settings: settings, SecureCookies: cfg.CookieSecure, AllowedOrigin: cfg.PublicOrigin, Ready: pool.Ping})
	return &App{Handler: h, close: func() {
		stopLeases()
		<-leaseDone
		m.Close()
		if rag != nil {
			_ = rag.Close()
		}
		closeApp()
	}}, nil
}
