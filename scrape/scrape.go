package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/choria-io/prometheus-streams/build"
	"github.com/choria-io/prometheus-streams/config"
	"github.com/choria-io/prometheus-streams/connection"
	"github.com/nats-io/go-nats-streaming"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var cfg *config.Config

type Scrape struct {
	Job       string `json:"job"`
	Instance  string `json:"instance"`
	Timestamp int64  `json:"time"`
	Publisher string `json:"publisher"`
	Scrape    []byte
}

var outbox = make(chan Scrape, 1000)
var restart = make(chan struct{}, 1)
var paused bool
var running bool
var stream *connection.Connection
var hostname string
var err error

func Run(ctx context.Context, wg *sync.WaitGroup, scrapeCfg *config.Config) {
	defer wg.Done()

	log.Infof("Choria Prometheus Streams Poller version %s starting with configuration file %s", build.Version, scrapeCfg.ConfigFile)

	running = true
	cfg = scrapeCfg

	stream, err = connect(ctx, scrapeCfg)
	if err != nil {
		log.Errorf("Could not start scrape: %s", err)
		return
	}

	jobsGauge.Set(float64(len(cfg.Jobs)))
	pauseGauge.Set(0)

	for name, job := range cfg.Jobs {
		wg.Add(1)
		go jobWorker(ctx, wg, name, job)
	}

	for {
		select {
		case <-restart:
			stream, err = connect(ctx, scrapeCfg)
			if err != nil {
				log.Errorf("Could not start scrape: %s", err)
				return
			}

		case m := <-outbox:
			publish(m)

		case <-ctx.Done():
			return
		}
	}
}

func connect(ctx context.Context, scrapeCfg *config.Config) (*connection.Connection, error) {
	stream, err = connection.NewConnection(ctx, scrapeCfg.PollerStream, func(_ stan.Conn, reason error) {
		errorCtr.Inc()
		log.Errorf("Stream connection disconnected, initiating reconnection: %s", reason)
		restart <- struct{}{}
	})

	if err != nil {
		return nil, fmt.Errorf("Could not start scrape: %s", err)
	}

	return stream, nil
}

func publish(m Scrape) {
	obs := prometheus.NewTimer(publishTime)
	defer obs.ObserveDuration()

	j, err := json.Marshal(m)
	if err != nil {
		log.Errorf("Could not publish data: %s", err)
		errorCtr.Inc()
		return
	}

	err = stream.Publish(cfg.PollerStream.Topic, j)
	if err != nil {
		log.Errorf("Could not publish data: %s", err)
		errorCtr.Inc()
		return
	}

	publishedCtr.Inc()

	log.Debugf("Published %d bytes to %s for job %s", len(j), cfg.PollerStream.Topic, m.Job)
}

func Paused() bool {
	return paused
}

func FlipCircuitBreaker() bool {
	paused = !paused

	if paused {
		pauseGauge.Set(1)
	} else {
		pauseGauge.Set(0)
	}

	if running {
		log.Warnf("Switching the circuit breaker: paused: %t", paused)
	}

	return Paused()
}

func Running() bool {
	return running
}
