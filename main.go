package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type KanbanItemReqRespBody struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
}

type KanbanGroupReqRespBody struct {
	Name  string                  `json:"groupName"`
	Items []KanbanItemReqRespBody `json:"items"`
}

type KanbanTableReqRespBody struct {
	TableName string                   `json:"tableName"`
	Groups    []KanbanGroupReqRespBody `json:"groups"`
}

// 10 random groups, each 20 random items
func RandomKanbanTable() KanbanTableReqRespBody {
	kanbanGroups := make([]KanbanGroupReqRespBody, 10)
	for i := range 10 {
		items := make([]KanbanItemReqRespBody, 20)
		for j := range 20 {
			items[j] = KanbanItemReqRespBody{
				ID:      int64(i*20 + j),
				Content: uuid.New().String(),
			}
		}

		kanbanGroups[i] = KanbanGroupReqRespBody{
			Name:  uuid.New().String(),
			Items: items,
		}
	}
	return KanbanTableReqRespBody{
		TableName: uuid.New().String(),
		Groups:    kanbanGroups,
	}
}

var (
	metricsClientCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "towd_bench_client_count",
		Help: "Number of client connections",
	})
	metricsLatencyPerRequest = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "towd_bench_latency_per_request",
		Help: "Latency per request",
	})
	metricsRequestsPerSecond = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "towd_bench_requests_per_second",
		Help: "Requests per second",
	})
)

func main() {
	randomKanbanTable := RandomKanbanTable()
	bodyBytes, _ := json.Marshal(randomKanbanTable)

	prometheus.Register(metricsClientCount)
	prometheus.Register(metricsLatencyPerRequest)
	prometheus.Register(metricsRequestsPerSecond)

	// collect all metricsLatencyPerRequest and calculate the average over 5 seconds
	latencyCh := make(chan float64, 10)
	go func() {
		latencies := make([]float64, 0)
		go func() {
			for range latencyCh {
				latencies = append(latencies, <-latencyCh)
			}
		}()

		ticker := time.NewTicker(5 * time.Second)
		for range ticker.C {
			totalRequestLatency := 0.0
			for _, latency := range latencies {
				totalRequestLatency += latency
			}
			metricsLatencyPerRequest.Set(totalRequestLatency / float64(len(latencies)))
			metricsRequestsPerSecond.Set(float64(len(latencies)) / 5.0)
			latencies = make([]float64, 0)
		}
	}()

	endpoint := os.Getenv("SERVER_BASE_URL")
	if endpoint == "" {
		panic("SERVER_BASE_URL is not set")
	}
	endpoint = strings.TrimSuffix("/", endpoint) + "/kanban/save"

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		panic("SESSION_SECRET is not set")
	}

	// spawn 10 clients continuously send requests
	// increase the number of clients every 5 minutes by 10
	currClientCount := 0
	go func() {
		for {
			for range 10 {
				go func() {
					req, _ := http.NewRequest("POST", endpoint, nil)
					req.Header.Set("Content-Type", "application/json")
					req.AddCookie(&http.Cookie{
						Name:  "session-secret",
						Value: sessionSecret,
					})
					req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

					for {
						startTimer := time.Now()
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							slog.Error("error sending request", "err", err)
							continue
						}
						_, err = io.ReadAll(resp.Body)
						resp.Body.Close()
						if err != nil {
							slog.Error("error reading response body", "err", err)
							continue
						}
						latencyCh <- float64(time.Since(startTimer).Microseconds())
					}
				}()
			}
			currClientCount += 10
			metricsClientCount.Set(float64(currClientCount))
			time.Sleep(10 * time.Minute)
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	if err := http.ListenAndServe(":2112", nil); err != nil {
		panic(err)
	}
}
