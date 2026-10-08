package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopilot/internal/agent"
	"gopilot/internal/api"
	"gopilot/internal/collector"
	"gopilot/internal/tools"
)

func main() {
	addr := environment("GOPILOT_ADDR", ":8080")
	demoServiceURL := environment("DEMO_SERVICE_URL", "http://localhost:8081")

	client, err := collector.NewClient(demoServiceURL, nil)
	if err != nil {
		log.Fatalf("configure demo service client: %v", err)
	}
	sampler, err := collector.NewSampler(client, collector.DefaultSampleInterval, collector.DefaultHistorySize)
	if err != nil {
		log.Fatalf("configure runtime sampler: %v", err)
	}

	processContext, stopProcess := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopProcess()
	go sampler.Run(processContext)

	toolService := tools.NewService(client, sampler)
	var diagnoser api.Diagnoser
	llmBaseURL := os.Getenv("LLM_BASE_URL")
	llmModel := os.Getenv("LLM_MODEL")
	if llmBaseURL != "" && llmModel != "" {
		var clientOptions []agent.ClientOption
		if thinking := os.Getenv("LLM_THINKING"); thinking != "" {
			clientOptions = append(clientOptions, agent.WithThinking(agent.ThinkingMode(thinking)))
		}
		llmClient, clientErr := agent.NewClient(llmBaseURL, os.Getenv("LLM_API_KEY"), llmModel, nil, clientOptions...)
		if clientErr != nil {
			log.Fatalf("configure LLM client: %v", clientErr)
		}
		agentRunner, runnerErr := agent.NewRunner(llmClient, toolService, agent.DefaultMaxSteps)
		if runnerErr != nil {
			log.Fatalf("configure agent runner: %v", runnerErr)
		}
		diagnoser = agentRunner
		log.Printf("diagnostic agent enabled with model %s", llmModel)
	} else {
		log.Printf("diagnostic agent disabled; set LLM_BASE_URL and LLM_MODEL to enable it")
	}
	apiServer := api.NewServer(toolService, diagnoser)
	server := &http.Server{
		Addr:              addr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("gopilot listening on %s; collecting from %s", addr, demoServiceURL)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve gopilot: %v", err)
		}
	}()

	<-processContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		log.Printf("shutdown gopilot: %v", err)
	}
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
