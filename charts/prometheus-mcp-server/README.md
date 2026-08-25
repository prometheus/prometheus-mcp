# prometheus-mcp-server

A Helm chart for deploying the [Prometheus MCP Server](https://github.com/tjhop/prometheus-mcp-server) to Kubernetes.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.x
- A running Prometheus instance accessible from the cluster

## Installation

### From OCI Registry

The chart is published as an OCI artifact to GitHub Container Registry on each release.

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --version <version> \
  --set prometheus.url=http://prometheus:9090
```

### From Source

```bash
git clone https://github.com/tjhop/prometheus-mcp-server.git
helm install prometheus-mcp-server charts/prometheus-mcp-server/ \
  --set prometheus.url=http://prometheus:9090
```

## Upgrading

```bash
helm upgrade prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --version <version>
```

## Uninstalling

```bash
helm uninstall prometheus-mcp-server
```

## Configuration

### Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `replicaCount` | int | `1` | Number of replicas |
| `image.repository` | string | `ghcr.io/tjhop/prometheus-mcp-server` | Container image repository |
| `image.tag` | string | `""` (appVersion) | Image tag override |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `imagePullSecrets` | list | `[]` | Image pull secrets for private registries |
| `nameOverride` | string | `""` | Override the chart name |
| `fullnameOverride` | string | `""` | Override the full release name |
| `prometheus.url` | string | `http://prometheus:9090` | URL of the Prometheus instance |
| `prometheus.backend` | string | `""` | Backend type (`""` for Prometheus, `"thanos"` for Thanos) |
| `prometheus.timeout` | string | `1m` | API call timeout (Go duration, e.g., `30s`, `2m`) |
| `prometheus.truncationLimit` | int | `0` | Max response size in lines (0 = disabled) |
| `mcp.transport` | string | `http` | MCP transport type (`http` or `stdio`) |
| `mcp.tools` | list | `["all"]` | Tools to load: `["all"]` for all tools, `["core"]` for core tools only, or a list of specific tool names |
| `mcp.enableToonOutput` | bool | `false` | Enable TOON output format |
| `mcp.enableClientLogging` | bool | `false` | Enable MCP client logging |
| `docs.autoUpdate` | bool | `false` | Enable automatic docs updates from prometheus/docs |
| `tsdbAdmin.enabled` | bool | `false` | Enable dangerous TSDB admin tools |
| `httpConfig.enabled` | bool | `false` | Enable Prometheus HTTP client config via Secret |
| `httpConfig.existingSecret` | string | `""` | Name of existing Secret containing `http-config.yaml` |
| `httpConfig.config` | object | `nil` | Prometheus HTTP client configuration content (stored in a Secret) |
| `log.level` | string | `info` | Log level (debug, info, warn, error) |
| `log.file` | string | `""` | Log file path (empty for stdout) |
| `containerPort` | int | `8080` | Container port (the port the process listens on, used for `--web.listen-address`) |
| `service.type` | string | `ClusterIP` | Service type |
| `service.port` | int | `8080` | Service port (exposed by the Kubernetes Service) |
| `service.annotations` | object | `{}` | Service annotations |
| `ingress.enabled` | bool | `false` | Enable ingress resource |
| `ingress.ingressClassName` | string | `""` | Ingress class name (required on K8s 1.22+ without a default IngressClass) |
| `ingress.annotations` | object | `{}` | Ingress annotations |
| `ingress.hosts` | list | see values.yaml | Ingress hosts configuration |
| `ingress.tls` | list | `[]` | Ingress TLS configuration |
| `httpRoute.enabled` | bool | `false` | Enable Gateway API HTTPRoute resource |
| `httpRoute.annotations` | object | `{}` | HTTPRoute annotations |
| `httpRoute.parentRefs` | list | `[]` | Parent gateway references (required when enabled) |
| `httpRoute.hostnames` | list | `[]` | Hostnames to match (matches all if empty) |
| `serviceMonitor.enabled` | bool | `false` | Enable ServiceMonitor for Prometheus Operator |
| `serviceMonitor.labels` | object | `{}` | Extra labels for serviceMonitorSelector matching |
| `serviceMonitor.namespace` | string | `""` | Namespace to deploy the ServiceMonitor into |
| `serviceMonitor.interval` | string | `""` | Scrape interval |
| `serviceMonitor.scrapeTimeout` | string | `""` | Scrape timeout |
| `serviceMonitor.namespaceSelector` | object | `{}` | Namespace selector |
| `serviceMonitor.relabelings` | list | `[]` | Relabelings before scraping |
| `serviceMonitor.metricRelabelings` | list | `[]` | Metric relabelings before ingestion |
| `serviceAccount.create` | bool | `true` | Create a service account |
| `serviceAccount.name` | string | `""` | Service account name |
| `serviceAccount.annotations` | object | `{}` | Service account annotations |
| `serviceAccount.automountServiceAccountToken` | bool | `false` | Auto-mount the service account token |
| `resources` | object | `{}` | Resource requests and limits |
| `podAnnotations` | object | `{}` | Pod annotations |
| `podLabels` | object | `{}` | Pod labels |
| `podSecurityContext` | object | see values.yaml | Pod security context (restricted PSS compliant) |
| `containerSecurityContext` | object | see values.yaml | Container security context (restricted PSS compliant) |
| `livenessProbe` | object | HTTP GET `/metrics` | Liveness probe configuration |
| `readinessProbe` | object | HTTP GET `/metrics` | Readiness probe configuration |
| `nodeSelector` | object | `{}` | Node selector |
| `tolerations` | list | `[]` | Tolerations |
| `affinity` | object | `{}` | Affinity rules |
| `extraArgs` | list | `[]` | Extra command-line arguments |
| `extraEnv` | list | `[]` | Extra environment variables |
| `extraVolumes` | list | `[]` | Extra volumes |
| `extraVolumeMounts` | list | `[]` | Extra volume mounts |
| `test.image.repository` | string | `busybox` | Test container image repository |
| `test.image.tag` | string | `"1.37"` | Test container image tag |
| `test.containerSecurityContext` | object | see values.yaml | Container security context for test pods |

### Examples

#### Basic install with custom Prometheus URL

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --set prometheus.url=http://my-prometheus:9090
```

#### Thanos backend

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --set prometheus.url=http://thanos-query:9090 \
  --set prometheus.backend=thanos
```

#### TLS and authentication via httpConfig

The HTTP client config is stored in a Kubernetes Secret since it may contain
credentials. You can either inline the config or reference an existing Secret:

```yaml
# custom-values.yaml (inline config -- stored in a chart-managed Secret)
prometheus:
  url: https://prometheus:9090

httpConfig:
  enabled: true
  config:
    basic_auth:
      username: admin
      password: secret
    tls_config:
      ca_file: /var/certs/ca.crt
      insecure_skip_verify: false

extraVolumes:
  - name: certs
    secret:
      secretName: prometheus-tls

extraVolumeMounts:
  - name: certs
    mountPath: /var/certs
    readOnly: true
```

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  -f custom-values.yaml
```

Alternatively, reference a pre-existing Secret (must contain an `http-config.yaml` key):

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --set prometheus.url=https://prometheus:9090 \
  --set httpConfig.enabled=true \
  --set httpConfig.existingSecret=my-http-config-secret
```

#### ServiceMonitor (Prometheus Operator)

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.release=kube-prometheus-stack
```

#### Ingress

```yaml
# ingress-values.yaml
ingress:
  enabled: true
  ingressClassName: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  hosts:
    - host: prometheus-mcp.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: prometheus-mcp-tls
      hosts:
        - prometheus-mcp.example.com
```

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  -f ingress-values.yaml
```

#### HTTPRoute (Gateway API)

```yaml
# httproute-values.yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: my-gateway
      namespace: default
  hostnames:
    - prometheus-mcp.example.com
```

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  -f httproute-values.yaml
```

#### Extra arguments

```bash
helm install prometheus-mcp-server oci://ghcr.io/tjhop/charts/prometheus-mcp-server \
  --set prometheus.url=http://prometheus:9090 \
  --set extraArgs[0]="--web.config.file=/etc/configs/web-config.yaml"
```
