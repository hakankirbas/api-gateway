#!/bin/bash

# =============================================================================
# Loki Manual Setup Script (No Helm Required)
# =============================================================================

set -euo pipefail

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
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

print_info "Setting up Loki Stack manually (without Helm)..."

# Step 1: Check prerequisites
print_info "Checking prerequisites..."
if ! command -v kubectl >/dev/null 2>&1; then
    print_error "kubectl is required but not found"
    exit 1
fi

if ! kubectl cluster-info >/dev/null 2>&1; then
    print_error "Cannot connect to Kubernetes cluster"
    exit 1
fi

print_success "Prerequisites checked"

# Step 2: Create namespace
print_info "Creating logging namespace..."
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Step 3: Apply Loki deployment
print_info "Deploying Loki stack..."

if [[ -f "k8s/logging/loki-complete-deployment.yaml" ]]; then
    kubectl apply -f k8s/logging/loki-complete-deployment.yaml
    print_success "Applied Loki deployment from k8s/logging/loki-complete-deployment.yaml"
else
    print_warning "k8s/logging/loki-complete-deployment.yaml not found. Creating minimal deployment..."
    
    # Create a minimal Loki deployment
    cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: logging

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: loki-config
  namespace: logging
data:
  loki.yaml: |
    auth_enabled: false
    server:
      http_listen_port: 3100
      grpc_listen_port: 9096
    common:
      path_prefix: /tmp/loki
      storage:
        filesystem:
          chunks_directory: /tmp/loki/chunks
          rules_directory: /tmp/loki/rules
      replication_factor: 1
      ring:
        instance_addr: 127.0.0.1
        kvstore:
          store: inmemory
    query_range:
      results_cache:
        cache:
          embedded_cache:
            enabled: true
            max_size_mb: 100
    schema_config:
      configs:
        - from: 2020-10-24
          store: boltdb-shipper
          object_store: filesystem
          schema: v11
          index:
            prefix: index_
            period: 24h
    limits_config:
      enforce_metric_name: false
      reject_old_samples: true
      reject_old_samples_max_age: 168h

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: loki
  namespace: logging
spec:
  replicas: 1
  selector:
    matchLabels:
      app: loki
  template:
    metadata:
      labels:
        app: loki
    spec:
      containers:
      - name: loki
        image: grafana/loki:2.9.0
        ports:
        - containerPort: 3100
          name: http-metrics
        args:
          - -config.file=/etc/loki/loki.yaml
        volumeMounts:
        - name: config
          mountPath: /etc/loki
        - name: storage
          mountPath: /tmp/loki
        resources:
          requests:
            memory: 256Mi
            cpu: 250m
          limits:
            memory: 512Mi
            cpu: 500m
      volumes:
      - name: config
        configMap:
          name: loki-config
      - name: storage
        emptyDir: {}

---
apiVersion: v1
kind: Service
metadata:
  name: loki
  namespace: logging
spec:
  ports:
  - port: 3100
    protocol: TCP
    name: http-metrics
    targetPort: http-metrics
  selector:
    app: loki

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: promtail-config
  namespace: logging
data:
  promtail.yaml: |
    server:
      http_listen_port: 3101
      grpc_listen_port: 0
    positions:
      filename: /tmp/positions.yaml
    clients:
      - url: http://loki:3100/loki/api/v1/push
    scrape_configs:
      - job_name: kubernetes-pods
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_name]
            target_label: pod_name
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_pod_label_app]
            target_label: app
          - replacement: /var/log/pods/*\$1/*.log
            separator: /
            source_labels: [__meta_kubernetes_pod_uid, __meta_kubernetes_pod_container_name]
            target_label: __path__
        pipeline_stages:
          - cri: {}
          - match:
              selector: '{app="api-gateway"}'
              stages:
                - json:
                    expressions:
                      level: level
                      correlation_id: correlation_id
                      component: component
                - labels:
                    level: level
                    correlation_id: correlation_id
                    component: component

---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: promtail
  namespace: logging
spec:
  selector:
    matchLabels:
      app: promtail
  template:
    metadata:
      labels:
        app: promtail
    spec:
      serviceAccount: promtail
      containers:
      - name: promtail
        image: grafana/promtail:2.9.0
        args:
        - -config.file=/etc/promtail/promtail.yaml
        - -client.url=http://loki:3100/loki/api/v1/push
        volumeMounts:
        - name: config
          mountPath: /etc/promtail
        - name: varlog
          mountPath: /var/log
          readOnly: true
        - name: varlibdockercontainers
          mountPath: /var/lib/docker/containers
          readOnly: true
        resources:
          requests:
            memory: 64Mi
            cpu: 50m
          limits:
            memory: 128Mi
            cpu: 100m
      volumes:
      - name: config
        configMap:
          name: promtail-config
      - name: varlog
        hostPath:
          path: /var/log
      - name: varlibdockercontainers
        hostPath:
          path: /var/lib/docker/containers
      tolerations:
      - key: node-role.kubernetes.io/master
        operator: Exists
        effect: NoSchedule

---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: promtail
  namespace: logging

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: promtail
rules:
- apiGroups: [""]
  resources: ["nodes", "nodes/proxy", "services", "endpoints", "pods"]
  verbs: ["get", "list", "watch"]

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: promtail
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: promtail
subjects:
- kind: ServiceAccount
  name: promtail
  namespace: logging

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: grafana
  namespace: logging
spec:
  replicas: 1
  selector:
    matchLabels:
      app: grafana
  template:
    metadata:
      labels:
        app: grafana
    spec:
      containers:
      - name: grafana
        image: grafana/grafana:9.5.0
        ports:
        - containerPort: 3000
          name: http
        env:
        - name: GF_SECURITY_ADMIN_PASSWORD
          value: "admin123"
        volumeMounts:
        - name: grafana-storage
          mountPath: /var/lib/grafana
        - name: grafana-datasources
          mountPath: /etc/grafana/provisioning/datasources
        resources:
          requests:
            memory: 128Mi
            cpu: 100m
          limits:
            memory: 256Mi
            cpu: 200m
      volumes:
      - name: grafana-storage
        emptyDir: {}
      - name: grafana-datasources
        configMap:
          name: grafana-datasources

---
apiVersion: v1
kind: Service
metadata:
  name: grafana
  namespace: logging
spec:
  ports:
  - port: 3000
    protocol: TCP
    name: http
    targetPort: http
    nodePort: 30300
  selector:
    app: grafana
  type: NodePort

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-datasources
  namespace: logging
data:
  datasource.yaml: |
    apiVersion: 1
    datasources:
      - name: Loki
        type: loki
        access: proxy
        url: http://loki:3100
        isDefault: true
        editable: true
EOF
    
    print_success "Minimal Loki deployment created"
fi

# Step 4: Wait for pods to be ready
print_info "Waiting for Loki stack to be ready..."
kubectl wait --for=condition=ready pod -l app=loki -n $NAMESPACE --timeout=300s || true
kubectl wait --for=condition=ready pod -l app=grafana -n $NAMESPACE --timeout=300s || true

# Step 5: Get service information
print_info "Getting service information..."
kubectl get pods -n $NAMESPACE
kubectl get svc -n $NAMESPACE

# Step 6: Create port-forwarding script
print_info "Creating port-forward script..."
cat > setup-port-forwards.sh << 'EOF'
#!/bin/bash
echo "Setting up port forwards for Loki stack..."

# Grafana
kubectl port-forward --namespace logging svc/grafana 3000:3000 &
GRAFANA_PID=$!

# Loki
kubectl port-forward --namespace logging svc/loki 3100:3100 &
LOKI_PID=$!

echo "Port forwards established:"
echo "  Grafana: http://localhost:3000 (admin/admin123)"
echo "  Loki: http://localhost:3100"
echo ""
echo "Press Ctrl+C to stop all port forwards"

# Wait for interrupt
trap "kill $GRAFANA_PID $LOKI_PID; exit" INT
wait
EOF

chmod +x setup-port-forwards.sh

print_success "Setup completed successfully!"
echo ""
print_info "Access Information:"
echo "  Grafana: http://localhost:3000 (after port-forward)"
echo "  Username: admin"
echo "  Password: admin123"
echo ""
print_info "To access services:"
echo "  ./setup-port-forwards.sh"
echo ""
print_info "Or use NodePort (if supported):"
echo "  Grafana: http://<node-ip>:30300"

# Step 7: Verify installation
print_info "Verifying installation..."
sleep 10
kubectl get pods -n $NAMESPACE

print_success "Loki stack setup completed!"
print_warning "Note: This is a basic setup. For production, consider using persistent volumes and proper resource limits."