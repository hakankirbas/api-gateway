#!/bin/bash

# =============================================================================
# Grafana Loki Complete Setup Script
# =============================================================================

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Configuration
NAMESPACE="logging"
LOKI_RELEASE_NAME="loki"
GRAFANA_RELEASE_NAME="grafana"
PROMETHEUS_RELEASE_NAME="prometheus"

print_info "Starting Grafana Loki setup..."

# Step 1: Create namespace
print_info "Creating logging namespace..."
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Step 2: Add Helm repositories
print_info "Adding Helm repositories..."
helm repo add grafana https://grafana.github.io/helm-charts
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Step 3: Install Loki Stack
print_info "Installing Loki Stack..."
cat > loki-values.yaml << 'EOF'
# Loki configuration
loki:
  enabled: true
  isDefault: true
  url: http://{{(include "loki.serviceName" .)}}:{{ .Values.loki.service.port }}
  readinessProbe:
    httpGet:
      path: /ready
      port: http-metrics
    initialDelaySeconds: 45
  livenessProbe:
    httpGet:
      path: /ready
      port: http-metrics
    initialDelaySeconds: 45
  datasource:
    jsonData: "{}"
    uid: ""

# Promtail configuration for log collection
promtail:
  enabled: true
  config:
    logLevel: info
    serverPort: 3101
    clients:
      - url: http://loki:3100/loki/api/v1/push

# Fluent Bit configuration (alternative to Promtail)
fluent-bit:
  enabled: true
  config:
    service: |
      [SERVICE]
          Flush 1
          Daemon Off
          Log_Level info
          HTTP_Server On
          HTTP_Listen 0.0.0.0
          HTTP_Port 2020
          Health_Check On

    inputs: |
      [INPUT]
          Name tail
          Path /var/log/containers/*api-gateway*.log
          multiline.parser docker, cri
          Tag api-gateway.*
          Mem_Buf_Limit 50MB
          Skip_Long_Lines On

      [INPUT]
          Name tail
          Path /var/log/containers/*.log
          multiline.parser docker, cri
          Tag kube.*
          Mem_Buf_Limit 50MB
          Skip_Long_Lines On
          Exclude_Path /var/log/containers/*_kube-system_*.log,/var/log/containers/*_logging_*.log

    filters: |
      [FILTER]
          Name kubernetes
          Match kube.*
          Kube_URL https://kubernetes.default.svc:443
          Kube_CA_File /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
          Kube_Token_File /var/run/secrets/kubernetes.io/serviceaccount/token
          Kube_Tag_Prefix kube.var.log.containers.
          Merge_Log On
          Merge_Log_Key log_processed
          K8S-Logging.Parser On
          K8S-Logging.Exclude Off

      [FILTER]
          Name kubernetes
          Match api-gateway.*
          Kube_URL https://kubernetes.default.svc:443
          Kube_CA_File /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
          Kube_Token_File /var/run/secrets/kubernetes.io/serviceaccount/token
          Kube_Tag_Prefix api-gateway.var.log.containers.
          Merge_Log On
          Merge_Log_Key log_processed
          K8S-Logging.Parser On
          K8S-Logging.Exclude Off

      [FILTER]
          Name parser
          Match api-gateway.*
          Key_Name log
          Parser json
          Reserve_Data On

    outputs: |
      [OUTPUT]
          Name loki
          Match *
          Host loki.logging.svc.cluster.local
          Port 3100
          Labels app=$kubernetes['labels']['app'], namespace=$kubernetes['namespace_name']
          Label_keys correlation_id,level,component,user_id

    parsers: |
      [PARSER]
          Name json
          Format json
          Time_Key timestamp
          Time_Format %Y-%m-%dT%H:%M:%S.%L%z

# Grafana configuration
grafana:
  enabled: true
  sidecar:
    datasources:
      enabled: true
      maxLines: 1000
  image:
    tag: 8.5.0
  admin:
    existingSecret: ""
    userKey: admin-user
    passwordKey: admin-password

# Prometheus for metrics (optional but recommended)
prometheus:
  enabled: true
  isDefault: false
  url: http://{{(include "prometheus.fullname" .)}}:{{ .Values.prometheus.server.service.servicePort }}{{ .Values.prometheus.server.prefixURL }}
  datasource:
    uid: ""

# Additional components
filebeat:
  enabled: false
logstash:
  enabled: false
EOF

# Install Loki Stack
helm install $LOKI_RELEASE_NAME grafana/loki-stack \
  --namespace $NAMESPACE \
  --values loki-values.yaml \
  --version 2.9.11

print_success "Loki Stack installed successfully!"

# Step 4: Wait for pods to be ready
print_info "Waiting for Loki Stack to be ready..."
kubectl wait --for=condition=ready pod -l app=loki -n $NAMESPACE --timeout=300s
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=grafana -n $NAMESPACE --timeout=300s

# Step 5: Get Grafana admin password
print_info "Retrieving Grafana admin password..."
GRAFANA_PASSWORD=$(kubectl get secret --namespace $NAMESPACE loki-grafana -o jsonpath="{.data.admin-password}" | base64 --decode)

print_success "Setup completed successfully!"
echo ""
print_info "Access Information:"
echo "  Grafana URL: http://localhost:3000 (after port-forward)"
echo "  Grafana Admin Username: admin"
echo "  Grafana Admin Password: $GRAFANA_PASSWORD"
echo ""
print_info "To access Grafana:"
echo "  kubectl port-forward --namespace $NAMESPACE svc/loki-grafana 3000:80"
echo ""
print_info "To access Loki directly:"
echo "  kubectl port-forward --namespace $NAMESPACE svc/loki 3100:3100"

# Step 6: Create port-forwarding script
cat > setup-port-forwards.sh << 'EOF'
#!/bin/bash
echo "Setting up port forwards for Loki stack..."

# Grafana
kubectl port-forward --namespace logging svc/loki-grafana 3000:80 &
GRAFANA_PID=$!

# Loki
kubectl port-forward --namespace logging svc/loki 3100:3100 &
LOKI_PID=$!

# Prometheus (if enabled)
kubectl port-forward --namespace logging svc/loki-prometheus-server 9090:80 &
PROMETHEUS_PID=$!

echo "Port forwards established:"
echo "  Grafana: http://localhost:3000"
echo "  Loki: http://localhost:3100"
echo "  Prometheus: http://localhost:9090"
echo ""
echo "Press Ctrl+C to stop all port forwards"

# Wait for interrupt
trap "kill $GRAFANA_PID $LOKI_PID $PROMETHEUS_PID; exit" INT
wait
EOF

chmod +x setup-port-forwards.sh

print_info "Port-forward script created: ./setup-port-forwards.sh"

# Step 7: Verify installation
print_info "Verifying installation..."
kubectl get pods -n $NAMESPACE
kubectl get svc -n $NAMESPACE

print_success "Grafana Loki setup completed successfully!"
print_warning "Don't forget to update your API Gateway to send logs to Loki!"

echo ""
print_info "Next steps:"
echo "1. Run ./setup-port-forwards.sh to access the services"
echo "2. Login to Grafana with admin/$GRAFANA_PASSWORD"
echo "3. Verify log ingestion in Grafana"
echo "4. Import provided dashboards"