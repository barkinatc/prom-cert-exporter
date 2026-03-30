package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Targets        []string      `yaml:"targets"`
	ScrapeInterval time.Duration `yaml:"scrape_interval"`
	ListenAddress  string        `yaml:"listen_address"`
}

type Exporter struct {
	configPath string
	timeout    time.Duration

	mu sync.RWMutex
	cfg Config

	certExpiryTimestamp *prometheus.GaugeVec
	certRemainingDays   *prometheus.GaugeVec
	probeSuccess        *prometheus.GaugeVec
	probeDuration       *prometheus.GaugeVec
}

func NewExporter(configPath string, timeout time.Duration) (*Exporter, error) {
	e := &Exporter{
		configPath: configPath,
		timeout:    timeout,
		certExpiryTimestamp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tls_cert_expiry_timestamp_seconds",
				Help: "TLS certificate expiry time as Unix timestamp.",
			},
			[]string{"target"},
		),
		certRemainingDays: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tls_cert_remaining_days",
				Help: "Remaining days until TLS certificate expiry.",
			},
			[]string{"target"},
		),
		probeSuccess: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tls_cert_probe_success",
				Help: "Whether probing the TLS endpoint succeeded.",
			},
			[]string{"target"},
		),
		probeDuration: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "tls_cert_probe_duration_seconds",
				Help: "Duration of the TLS probe in seconds.",
			},
			[]string{"target"},
		),
	}

	if err := e.reloadConfig(); err != nil {
		return nil, err
	}

	return e, nil
}

func (e *Exporter) reloadConfig() error {
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		return fmt.Errorf("config read failed: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("config parse failed: %w", err)
	}

	if cfg.ScrapeInterval <= 0 {
		cfg.ScrapeInterval = 30 * time.Second
	}
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = ":2112"
	}

	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()

	return nil
}

func (e *Exporter) currentConfig() Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

func normalizeTarget(raw string) (host string, serverName string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}

	switch u.Scheme {
	case "", "https":
	default:
		return "", "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	if u.Host == "" {
		u, err = url.Parse("https://" + raw)
		if err != nil {
			return "", "", err
		}
	}

	hostname := u.Hostname()
	if hostname == "" {
		return "", "", errors.New("empty hostname")
	}

	port := u.Port()
	if port == "" {
		port = "443"
	}

	return net.JoinHostPort(hostname, port), hostname, nil
}

func (e *Exporter) probeTarget(ctx context.Context, target string) {
	start := time.Now()

	address, serverName, err := normalizeTarget(target)
	if err != nil {
		e.probeSuccess.WithLabelValues(target).Set(0)
		e.probeDuration.WithLabelValues(target).Set(time.Since(start).Seconds())
		e.certExpiryTimestamp.WithLabelValues(target).Set(0)
		e.certRemainingDays.WithLabelValues(target).Set(0)
		log.Printf("target %q invalid: %v", target, err)
		return
	}

	dialer := &net.Dialer{Timeout: e.timeout}

	conn, err := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		e.probeSuccess.WithLabelValues(target).Set(0)
		e.probeDuration.WithLabelValues(target).Set(time.Since(start).Seconds())
		e.certExpiryTimestamp.WithLabelValues(target).Set(0)
		e.certRemainingDays.WithLabelValues(target).Set(0)
		log.Printf("target %q probe failed: %v", target, err)
		return
	}
	defer conn.Close()

	select {
	case <-ctx.Done():
		e.probeSuccess.WithLabelValues(target).Set(0)
		e.probeDuration.WithLabelValues(target).Set(time.Since(start).Seconds())
		e.certExpiryTimestamp.WithLabelValues(target).Set(0)
		e.certRemainingDays.WithLabelValues(target).Set(0)
		return
	default:
	}

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		e.probeSuccess.WithLabelValues(target).Set(0)
		e.probeDuration.WithLabelValues(target).Set(time.Since(start).Seconds())
		e.certExpiryTimestamp.WithLabelValues(target).Set(0)
		e.certRemainingDays.WithLabelValues(target).Set(0)
		log.Printf("target %q returned no certificates", target)
		return
	}

	notAfter := certs[0].NotAfter
	remainingDays := time.Until(notAfter).Hours() / 24

	e.certExpiryTimestamp.WithLabelValues(target).Set(float64(notAfter.Unix()))
	e.certRemainingDays.WithLabelValues(target).Set(remainingDays)
	e.probeSuccess.WithLabelValues(target).Set(1)
	e.probeDuration.WithLabelValues(target).Set(time.Since(start).Seconds())
}

func (e *Exporter) collectOnce() {
	if err := e.reloadConfig(); err != nil {
		log.Printf("config reload failed: %v", err)
	}

	cfg := e.currentConfig()
	var wg sync.WaitGroup

	for _, target := range cfg.Targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
			defer cancel()
			e.probeTarget(ctx, target)
		}()
	}

	wg.Wait()
}

func (e *Exporter) Run() error {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		e.certExpiryTimestamp,
		e.certRemainingDays,
		e.probeSuccess,
		e.probeDuration,
	)

	cfg := e.currentConfig()

	go func() {
		ticker := time.NewTicker(cfg.ScrapeInterval)
		defer ticker.Stop()

		e.collectOnce()

		for range ticker.C {
			e.collectOnce()
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("listening on %s", cfg.ListenAddress)
	return http.ListenAndServe(cfg.ListenAddress, mux)
}

func main() {
	configPath := "config.yaml"
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		configPath = v
	}

	exporter, err := NewExporter(configPath, 10*time.Second)
	if err != nil {
		log.Fatal(err)
	}

	if err := exporter.Run(); err != nil {
		log.Fatal(err)
	}
}