# Prometheus scraping

The gateway exposes Prometheus metrics on `127.0.0.1:8091` (see
`GATEWAY_METRICS_ADDR`). The metrics are counters held in memory, so they reset
whenever the service restarts. To keep a history of requests, tokens and
latency per client, the server's Prometheus (snap `prometheus` 2.37, listening
on `:9090`) scrapes that endpoint.

## Install the scrape job

Run this as a user with sudo rights:

```bash
sudo cp -a /var/snap/prometheus/current/prometheus.yml /var/snap/prometheus/current/prometheus.yml.bak-$(date +%F) && \
sudo tee /var/snap/prometheus/current/prometheus.yml > /dev/null <<'EOF' &&
# my global config
global:
  scrape_interval: 15s # Set the scrape interval to every 15 seconds. Default is every 1 minute.
  evaluation_interval: 15s # Evaluate rules every 15 seconds. The default is every 1 minute.
  # scrape_timeout is set to the global default (10s).

# Alertmanager configuration
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          # - alertmanager:9093

# Load rules once and periodically evaluate them according to the global 'evaluation_interval'.
rule_files:
  # - "first_rules.yml"
  # - "second_rules.yml"

# A scrape configuration containing exactly one endpoint to scrape:
# Here it's Prometheus itself.
scrape_configs:
  # The job name is added as a label `job=<job_name>` to any timeseries scraped from this config.
  - job_name: "prometheus"

    # metrics_path defaults to '/metrics'
    # scheme defaults to 'http'.

    static_configs:
      - targets: ["localhost:9090"]

  # llm-gateway (proyectos-go): per-client requests, tokens and latency.
  - job_name: "llm-gateway"
    static_configs:
      - targets: ["127.0.0.1:8091"]
EOF
sudo /snap/prometheus/current/bin/promtool check config /var/snap/prometheus/current/prometheus.yml && \
sudo pkill -HUP -f '/snap/prometheus/[0-9]+/bin/prometheus --config'
```

What each step does:

1. **Backup.** Copies the current config to `prometheus.yml.bak-YYYY-MM-DD`,
   keeping its owner and permissions (`-a`).
2. **Write the new config.** `sudo tee` replaces the file with the original
   content plus the `llm-gateway` job at the end. A plain `>` redirect would
   not work, because the shell opens the file before `sudo` runs. The quoted
   `'EOF'` stops the shell from expanding anything inside the text.
3. **Validate.** `promtool check config` checks the syntax. If it fails, the
   chain stops and Prometheus keeps running with the config it already loaded.
   To undo the change, restore the backup.
4. **Reload.** `SIGHUP` tells Prometheus to reread its config without a
   restart, so no data is lost. The HTTP reload endpoint
   (`--web.enable-lifecycle`) is disabled on this server, so the signal is the
   way to do it.

The `current` path is a symlink to the active snap revision, so the command
keeps working after a snap refresh.

## Verify

```bash
curl -s 'http://localhost:9090/api/v1/targets?state=active' | grep -o '"job":"llm-gateway"[^}]*"health":"[a-z]*"'
curl -s 'http://localhost:9090/api/v1/query?query=gateway_requests_total'
```

The target should report `"health":"up"`, and the query should return the
`gateway_requests_total` series by client and status code.

## Retention

Prometheus runs with its default retention of **15 days**. Keeping a longer
history means changing the snap's startup flags (`--storage.tsdb.retention.time`).
That is a separate decision.
