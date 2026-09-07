#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)

set -a
source "$script_dir/versions.env"
set +a

run_id=${CALYPR_RUN_ID:-${GITHUB_RUN_ID:-local}}
cluster_name=${CALYPR_CLUSTER_NAME:-syfon-calypr-$run_id}
namespace=${CALYPR_NAMESPACE:-test}
state_dir=${CALYPR_STATE_DIR:-${TMPDIR:-/tmp}/syfon-calypr-$run_id}
artifacts_dir=${CALYPR_ARTIFACTS_DIR:-$state_dir/artifacts}
chart_dir=$state_dir/gen3-helm
export KUBECONFIG=$state_dir/kubeconfig

log() {
  printf '[calypr-ci] %s\n' "$*"
}

fail() {
  printf '[calypr-ci] ERROR: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

record_time() {
  mkdir -p "$artifacts_dir"
  printf '%s\t%s\n' "$1" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$artifacts_dir/timings.tsv"
}

write_state() {
  mkdir -p "$state_dir" "$artifacts_dir"
  {
    printf 'CALYPR_CLUSTER_NAME=%q\n' "$cluster_name"
    printf 'CALYPR_NAMESPACE=%q\n' "$namespace"
    printf 'KUBECONFIG=%q\n' "$KUBECONFIG"
    printf 'CALYPR_ARTIFACTS_DIR=%q\n' "$artifacts_dir"
    printf 'GEN3_HELM_COMMIT=%q\n' "$GEN3_HELM_COMMIT"
    printf 'GIT_DRS_COMMIT=%q\n' "$GIT_DRS_COMMIT"
  } >"$state_dir/run.env"
}

preflight() {
  for command_name in docker git helm jq kind kubectl openssl; do
    require_command "$command_name"
  done
  docker info >/dev/null
  docker_arch=$(docker version --format '{{.Server.Arch}}')
  case "$docker_arch" in
    amd64 | x86_64 | arm64 | aarch64) ;;
    *) fail "Calypr CI supports amd64 and arm64 Docker servers (found $docker_arch)" ;;
  esac
  test -f "$repo_root/Dockerfile" || fail "run from a Syfon checkout"
  test -f "$script_dir/values-ci.yaml" || fail "missing values-ci.yaml"
}

prepare_chart() {
  log "fetching gen3-helm $GEN3_HELM_BRANCH at $GEN3_HELM_COMMIT"
  git clone --quiet --no-checkout --filter=blob:none "$GEN3_HELM_REPOSITORY" "$chart_dir"
  git -C "$chart_dir" fetch --quiet --depth 1 origin "$GEN3_HELM_COMMIT"
  git -C "$chart_dir" checkout --quiet --detach "$GEN3_HELM_COMMIT"
  test "$(git -C "$chart_dir" rev-parse HEAD)" = "$GEN3_HELM_COMMIT"
  cp "$state_dir/tls.crt" "$chart_dir/helm/revproxy/ssl/service.crt"
  cp "$state_dir/tls.key" "$chart_dir/helm/revproxy/ssl/service.key"
  helm dependency build "$chart_dir/helm/gen3" >/dev/null
}

generate_certificates() {
  cat >"$state_dir/openssl.cnf" <<'EOF'
[req]
distinguished_name = dn
prompt = no
[dn]
CN = calypr.test
[server]
subjectAltName = @alt_names
basicConstraints = CA:FALSE
keyUsage = digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth
[alt_names]
DNS.1 = calypr.test
DNS.2 = calypr.test.svc
DNS.3 = calypr.test.svc.cluster.local
EOF
  openssl genrsa -out "$state_dir/ca.key" 2048 >/dev/null 2>&1
  openssl req -x509 -new -key "$state_dir/ca.key" -sha256 -days 1 \
    -subj '/CN=Syfon Calypr CI CA' -out "$state_dir/ca.crt"
  openssl genrsa -out "$state_dir/tls.key" 2048 >/dev/null 2>&1
  openssl req -new -key "$state_dir/tls.key" -config "$state_dir/openssl.cnf" \
    -out "$state_dir/tls.csr"
  openssl x509 -req -in "$state_dir/tls.csr" -CA "$state_dir/ca.crt" \
    -CAkey "$state_dir/ca.key" -CAcreateserial -days 1 -sha256 \
    -extensions server -extfile "$state_dir/openssl.cnf" -out "$state_dir/tls.crt"
}

build_candidate() {
  log "building candidate Syfon image $SYFON_IMAGE"
  docker build --tag "$SYFON_IMAGE" "$repo_root"
}

build_client() {
  log "building git-drs $GIT_DRS_BRANCH client at $GIT_DRS_COMMIT"
  docker build --tag "$CLIENT_IMAGE" \
    --file "$script_dir/client.Dockerfile" \
    --build-arg "GIT_DRS_REPOSITORY=$GIT_DRS_REPOSITORY" \
    --build-arg "GIT_DRS_BRANCH=$GIT_DRS_BRANCH" \
    --build-arg "GIT_DRS_COMMIT=$GIT_DRS_COMMIT" \
    "$repo_root"
}

create_cluster() {
  log "creating kind cluster $cluster_name"
  kind create cluster \
    --name "$cluster_name" \
    --image "$KIND_NODE_IMAGE" \
    --config "$script_dir/kind.yaml" \
    --kubeconfig "$KUBECONFIG"
}

load_images() {
  kind load docker-image "$SYFON_IMAGE" "$CLIENT_IMAGE" \
    --name "$cluster_name"
}

deploy_infrastructure() {
  kubectl create namespace "$namespace"
  kubectl --namespace "$namespace" apply -f "$script_dir/infrastructure.yaml"
  kubectl --namespace "$namespace" create secret generic calypr-test-ca \
    --from-file=ca.crt="$state_dir/ca.crt"
  for deployment_name in postgres minio gogs; do
    kubectl --namespace "$namespace" wait --for=condition=Available \
      "deployment/$deployment_name" --timeout=180s
  done
  provision_postgres_databases
  wait_for_job minio-bootstrap 120 || fail 'MinIO bootstrap job failed'
}

provision_postgres_databases() {
  postgres_pod=$(kubectl --namespace "$namespace" get pod \
    -l app=postgres -o jsonpath='{.items[0].metadata.name}')
  test -n "$postgres_pod" || fail 'PostgreSQL pod was not found after deployment became available'
  log 'provisioning Fence and Arborist databases in the existing PostgreSQL pod'

  for service_db in fence arborist; do
    case "$service_db" in
      fence)
        service_user=fence
        service_pass=fence-ci
        ;;
      arborist)
        service_user=arborist
        service_pass=arborist-ci
        ;;
      *)
        fail "unsupported Calypr database: $service_db"
        ;;
    esac

    # Keep this SQL aligned with gen3-helm's common.db_setup_job. psql's
    # identifier and literal variables keep this idempotent and safe if a
    # retained cluster is repaired and provisioned again.
    kubectl --namespace "$namespace" exec --stdin "$postgres_pod" -- \
      env PGPASSWORD=postgres-ci psql -h 127.0.0.1 -p 5432 -U postgres \
      -d postgres -v ON_ERROR_STOP=1 \
      -v service_user="$service_user" \
      -v service_db="$service_db" \
      -v service_pass="$service_pass" -f - <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'service_user', :'service_pass')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'service_user')\gexec
ALTER ROLE :"service_user" WITH LOGIN PASSWORD :'service_pass';
SELECT format('CREATE DATABASE %I OWNER %I', :'service_db', :'service_user')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'service_db')\gexec
ALTER DATABASE :"service_db" OWNER TO :"service_user";
GRANT ALL ON DATABASE :"service_db" TO :"service_user" WITH GRANT OPTION;
SQL

    kubectl --namespace "$namespace" exec --stdin "$postgres_pod" -- \
      env PGPASSWORD=postgres-ci psql -h 127.0.0.1 -p 5432 -U postgres \
      -d "$service_db" -v ON_ERROR_STOP=1 \
      -v service_user="$service_user" -f - <<'SQL'
CREATE EXTENSION IF NOT EXISTS ltree;
ALTER ROLE :"service_user" WITH LOGIN;
GRANT ALL ON SCHEMA public TO :"service_user";
ALTER SCHEMA public OWNER TO :"service_user";
SQL
  done
}

mark_database_secrets_ready() {
  for secret_name in fence-dbcreds arborist-dbcreds; do
    kubectl --namespace "$namespace" get "secret/$secret_name" >/dev/null || \
      fail "Helm did not create $secret_name"
    # gen3 services gate startup on this exact value (base64(true\n)).
    kubectl --namespace "$namespace" patch "secret/$secret_name" \
      --type merge --patch '{"data":{"dbcreated":"dHJ1ZQo="}}' >/dev/null
  done
}

deploy_calypr() {
  log "deploying Calypr services from gen3-helm $GEN3_HELM_COMMIT"
  helm upgrade --install calypr "$chart_dir/helm/gen3" \
    --namespace "$namespace" \
    --values "$script_dir/values-ci.yaml" \
    --set-file global.tls.cert="$state_dir/tls.crt" \
    --set-file global.tls.key="$state_dir/tls.key" \
    --timeout 8m
  kubectl --namespace "$namespace" patch job useryaml \
    --type merge --patch '{"spec":{"suspend":true}}' >/dev/null
  mark_database_secrets_ready
  kubectl --namespace "$namespace" patch deployment syfon \
    --type strategic --patch-file "$script_dir/syfon-ca-patch.yaml"
  for deployment_name in arborist-deployment fence-deployment calypr-redis-deployment revproxy-deployment; do
    kubectl --namespace "$namespace" rollout status \
      "deployment/$deployment_name" --timeout=300s
  done
  kubectl --namespace "$namespace" rollout status deployment/syfon --timeout=180s
  kubectl --namespace "$namespace" patch job useryaml \
    --type merge --patch '{"spec":{"suspend":false}}' >/dev/null
  bootstrap_jobs=()
  while IFS= read -r job_name; do
    test -n "$job_name" && bootstrap_jobs+=("$job_name")
  done < <(kubectl --namespace "$namespace" get jobs -o json | jq -r '.items[].metadata.name')
  test "${#bootstrap_jobs[@]}" -gt 0 || fail 'no Calypr bootstrap jobs found'
  wait_for_jobs 300 "${bootstrap_jobs[@]}" || fail 'a Calypr bootstrap job failed'
}

bootstrap_gogs() {
  log 'creating deterministic Gogs user and repository'
  gogs_pod=$(kubectl --namespace "$namespace" get pod \
    -l app=gogs -o jsonpath='{.items[0].metadata.name}')
  kubectl --namespace "$namespace" exec "$gogs_pod" -- \
    gosu git /app/gogs/gogs admin create-user \
    --name ci-git \
    --password gogs-ci \
    --email syfon-ci@invalid \
    --admin \
    --config /data/gogs/conf/app.ini
  kubectl --namespace "$namespace" run gogs-bootstrap \
    --image="$CLIENT_IMAGE" \
    --image-pull-policy=Never \
    --restart=Never \
    --command -- /bin/bash -c '
      set -euo pipefail
      token_json=$(curl --fail --silent --show-error \
        -u ci-git:gogs-ci \
        -H '"'"'Content-Type: application/json'"'"' \
        -d '"'"'{"name":"git-drs-e2e-token"}'"'"' \
        http://gogs.test:3000/api/v1/users/ci-git/tokens)
      gogs_token=$(printf '"'"'%s'"'"' "$token_json" | jq -er '"'"'.sha1 | select(type == "string" and length > 0)'"'"')
      repo_json=$(curl --fail --silent --show-error \
        -H "Authorization: token $gogs_token" \
        -H '"'"'Content-Type: application/json'"'"' \
        -d '"'"'{"name":"syfon-fixture","auto_init":false,"private":false}'"'"' \
        http://gogs.test:3000/api/v1/user/repos)
      printf '"'"'%s'"'"' "$repo_json" | jq -e '"'"'.clone_url | select(type == "string" and length > 0)'"'"' >/dev/null
    '
  wait_for_pod_success gogs-bootstrap 90 || fail 'Gogs bootstrap pod failed'
}

fence_token() {
  username=$1
  expiration=$2
  fence_pod=$(kubectl --namespace "$namespace" get pod \
    -l app.kubernetes.io/name=fence -o jsonpath='{.items[0].metadata.name}')
  kubectl --namespace "$namespace" exec "$fence_pod" -- \
    fence-create token-create \
    --type access \
    --username "$username" \
    --scopes openid,user,data \
    --exp "$expiration" \
    --keys-dir /fence/keys/key | tail -1
}

wait_for_job() {
  job_name=$1
  timeout_seconds=$2
  wait_for_jobs "$timeout_seconds" "$job_name"
}

wait_for_pod_success() {
  pod_name=$1
  timeout_seconds=$2
  deadline=$((SECONDS + timeout_seconds))
  while ((SECONDS < deadline)); do
    pod_json=$(kubectl --namespace "$namespace" get "pod/$pod_name" -o json 2>/dev/null || true)
    if test -n "$pod_json"; then
      pod_phase=$(jq -r '.status.phase // empty' <<<"$pod_json")
      case "$pod_phase" in
        Succeeded)
          return 0
          ;;
        Failed)
          kubectl --namespace "$namespace" logs "pod/$pod_name" --all-containers=true >&2 || true
          return 1
          ;;
      esac
    fi
    sleep 2
  done
  kubectl --namespace "$namespace" logs "pod/$pod_name" --all-containers=true >&2 || true
  return 1
}

wait_for_jobs() {
  timeout_seconds=$1
  shift
  job_names=("$@")
  deadline=$((SECONDS + timeout_seconds))
  while ((SECONDS < deadline)); do
    all_complete=1
    for job_name in "${job_names[@]}"; do
      job_status=$(kubectl --namespace "$namespace" get "job/$job_name" -o json)
      if jq -e '.status.conditions[]? | select(.type == "Failed" and .status == "True")' \
        >/dev/null <<<"$job_status"; then
        kubectl --namespace "$namespace" logs "job/$job_name" --all-containers=true >&2 || true
        return 1
      fi
      if ! jq -e '.status.conditions[]? | select(.type == "Complete" and .status == "True")' \
        >/dev/null <<<"$job_status"; then
        all_complete=0
      fi
    done
    if test "$all_complete" -eq 1; then
      return 0
    fi
    sleep 2
  done
  for job_name in "${job_names[@]}"; do
    kubectl --namespace "$namespace" logs "job/$job_name" --all-containers=true >&2 || true
  done
  return 1
}

bootstrap_tokens() {
  log 'issuing short-lived tokens from the deployed Fence image'
  writer_token=$(fence_token ci-writer 2400)
  reader_token=$(fence_token ci-reader 2400)
  outsider_token=$(fence_token ci-outsider 2400)
  expired_token=$(fence_token ci-reader -60)
  for token in "$writer_token" "$reader_token" "$outsider_token" "$expired_token"; do
    test "$(printf '%s' "$token" | awk -F. '{print NF}')" -eq 3 || fail 'Fence returned a malformed token'
  done
  umask 077
  {
    printf 'CI_WRITER_TOKEN=%s\n' "$writer_token"
    printf 'CI_READER_TOKEN=%s\n' "$reader_token"
    printf 'CI_OUTSIDER_TOKEN=%s\n' "$outsider_token"
    printf 'CI_EXPIRED_TOKEN=%s\n' "$expired_token"
  } >"$state_dir/tokens.env"
  kubectl --namespace "$namespace" create secret generic calypr-user-tokens \
    --from-env-file="$state_dir/tokens.env"
}

record_images() {
  kubectl --namespace "$namespace" get pods -o json | jq '{pods: [.items[] | {name: .metadata.name, containers: [.status.containerStatuses[]? | {name, image, imageID}]}]}' \
    >"$artifacts_dir/images.json"
  candidate_id=$(docker image inspect "$SYFON_IMAGE" --format '{{.Id}}')
  printf '%s\n' "$candidate_id" >"$artifacts_dir/candidate-image-id.txt"
  kubectl --namespace "$namespace" get deployment syfon \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="syfon")].image}' | \
    grep -Fx "$SYFON_IMAGE" >/dev/null || fail 'deployed Syfon image is not the candidate image'
}

run_scenario() {
  scenario=$1
  job=$2
  marker=$3
  record_time "$scenario-start"
  kubectl --namespace "$namespace" apply \
    -f "$script_dir/scenario-jobs.yaml" \
    -l "calypr.syfon/scenario=$scenario"
  if ! wait_for_job "$job" 600; then
    kubectl --namespace "$namespace" logs "job/$job" --all-containers=true >"$artifacts_dir/$job.log" 2>&1 || true
    fail "$scenario scenario failed"
  fi
  kubectl --namespace "$namespace" logs "job/$job" --all-containers=true | \
    tee "$artifacts_dir/$job.log"
  grep -F "SCENARIO $marker PASS" "$artifacts_dir/$job.log" >/dev/null || \
    fail "$scenario completed without required marker: $marker"
  record_time "$scenario-complete"
}

restart_syfon() {
  old_pods=$(kubectl --namespace "$namespace" get pods \
    -l app.kubernetes.io/name=syfon -o json)
  old_pod=$(jq -er 'first(.items[] | .metadata.name) // error("Syfon pod was not found before restart")' <<<"$old_pods")
  old_uid=$(jq -er 'first(.items[] | .metadata.uid) // error("Syfon pod UID was not found before restart")' <<<"$old_pods")
  kubectl --namespace "$namespace" delete pod "$old_pod" --wait=false
  # Deleting a Pod from the current ReplicaSet does not create a Deployment
  # revision, so rollout status alone can observe the old Pod as available.
  kubectl --namespace "$namespace" wait \
    --for=delete "pod/$old_pod" --timeout=180s
  kubectl --namespace "$namespace" rollout status deployment/syfon --timeout=180s
  new_pods=$(kubectl --namespace "$namespace" get pods \
    -l app.kubernetes.io/name=syfon -o json)
  new_pod=$(jq -er --arg old_uid "$old_uid" '
    [.items[] | select(.metadata.uid != $old_uid) | .metadata.name]
    | if length > 0 then .[0]
      else error("Syfon deployment has no replacement pod")
      end
  ' <<<"$new_pods")
  kubectl --namespace "$namespace" wait \
    --for=condition=Ready "pod/$new_pod" --timeout=180s
  new_uid=$(kubectl --namespace "$namespace" get pod "$new_pod" \
    -o jsonpath='{.metadata.uid}')
  test "$old_uid" != "$new_uid" || fail 'Syfon pod was not replaced'
}

validate_results() {
  required=(
    bootstrap
    managed-upload
    single-part-upload
    multipart-upload
    fresh-clone-download
    single-part-download
    multipart-download
    signed-download
    signature-rejection
    unsigned-storage-denial
    cross-project-denial
    read-only-upload-denial
    expired-token-denial
    storage-side-effects
    restart-persistence
  )
  : >"$artifacts_dir/scenarios.tsv"
  for marker in "${required[@]}"; do
    if ! grep -R -F "SCENARIO $marker PASS" "$artifacts_dir"/*.log >/dev/null; then
      fail "required scenario did not run: $marker"
    fi
    printf '%s\tPASS\n' "$marker" >>"$artifacts_dir/scenarios.tsv"
  done
  fence_image=$(kubectl --namespace "$namespace" get deployment fence-deployment \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="fence")].image}')
  arborist_image=$(kubectl --namespace "$namespace" get deployment arborist-deployment \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="arborist")].image}')
  jq -n \
    --arg chart "$GEN3_HELM_COMMIT" \
    --arg git_drs "$GIT_DRS_COMMIT" \
    --arg fence "$fence_image" \
    --arg arborist "$arborist_image" \
    --rawfile scenarios "$artifacts_dir/scenarios.tsv" \
    '{gen3_helm_commit:$chart,git_drs_commit:$git_drs,fence_image:$fence,arborist_image:$arborist,scenarios:($scenarios|split("\n")|map(select(length>0)|split("\t")|{name:.[0],result:.[1]}))}' \
    >"$artifacts_dir/results.json"
  record_time suite-complete
}

up() {
  preflight
  write_state
  test ! -e "$KUBECONFIG" || fail "state already exists at $state_dir; run down or set CALYPR_RUN_ID"
  record_time suite-start
  generate_certificates
  prepare_chart &
  chart_pid=$!
  build_candidate &
  candidate_pid=$!
  build_client &
  client_pid=$!
  create_cluster &
  cluster_pid=$!
  for pid in "$chart_pid" "$candidate_pid" "$client_pid" "$cluster_pid"; do
    wait "$pid"
  done
  record_time cluster-and-images-ready
  load_images
  record_time candidate-images-loaded
  deploy_infrastructure
  record_time infrastructure-ready
  deploy_calypr
  record_time calypr-services-ready
  bootstrap_gogs
  bootstrap_tokens
  record_images
  kubectl --namespace "$namespace" apply -f "$script_dir/scenario-scripts.yaml"
  record_time services-and-users-ready
}

test_scenarios() {
  test -f "$KUBECONFIG" || fail "cluster state not found at $state_dir"
  mkdir -p "$artifacts_dir"
  run_scenario bootstrap calypr-bootstrap-check bootstrap
  run_scenario writer calypr-git-drs managed-upload
  run_scenario reader calypr-reader fresh-clone-download
  run_scenario denial calypr-denial cross-project-denial
  run_scenario storage calypr-storage-check storage-side-effects
  restart_syfon
  run_scenario restart calypr-restart-reader restart-persistence
  validate_results
}

diagnose() {
  mkdir -p "$artifacts_dir"
  if test ! -f "$KUBECONFIG"; then
    log "no kubeconfig at $KUBECONFIG"
    return
  fi
  kubectl --namespace "$namespace" get pods -o wide >"$artifacts_dir/pods.txt" 2>&1 || true
  kubectl --namespace "$namespace" get events --sort-by=.lastTimestamp >"$artifacts_dir/events.txt" 2>&1 || true
  kubectl --namespace "$namespace" describe pods >"$artifacts_dir/pod-descriptions.txt" 2>&1 || true
  for object in deployment/fence-deployment deployment/arborist-deployment deployment/syfon deployment/revproxy-deployment job/useryaml; do
    safe_name=${object//\//-}
    kubectl --namespace "$namespace" logs "$object" --all-containers=true --prefix \
      >"$artifacts_dir/$safe_name.log" 2>&1 || true
  done
  log "diagnostics saved to $artifacts_dir"
}

down() {
  if kind get clusters 2>/dev/null | grep -Fx "$cluster_name" >/dev/null; then
    kind delete cluster --name "$cluster_name"
  fi
}

all() {
  set +e
  "$script_dir/run.sh" up
  result=$?
  if test "$result" -eq 0; then
    "$script_dir/run.sh" test
    result=$?
  fi
  if test "$result" -ne 0; then
    "$script_dir/run.sh" diagnose || true
  fi
  if test "${CALYPR_KEEP_CLUSTER:-0}" != 1; then
    "$script_dir/run.sh" down
    cleanup_result=$?
    if test "$result" -eq 0 && test "$cleanup_result" -ne 0; then
      result=$cleanup_result
    fi
  else
    log "cluster retained: $cluster_name"
    log "KUBECONFIG=$KUBECONFIG"
  fi
  set -e
  return "$result"
}

case "${1:-}" in
  up) up ;;
  test) test_scenarios ;;
  diagnose) diagnose ;;
  down) down ;;
  all) all ;;
  *) fail 'usage: tests/calypr/run.sh {up|test|diagnose|down|all}' ;;
esac
