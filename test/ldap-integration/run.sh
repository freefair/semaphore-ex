#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_dir="$(cd -- "$script_dir/../.." && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/semaphore-ldap-integration.XXXXXX")"
compose_project="semaphore-ldap-$$"
compose_started=false

cleanup() {
  if [[ "$compose_started" == true ]]; then
    docker compose --project-name "$compose_project" --file "$script_dir/compose.yaml" \
      down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/semaphore-ldap-integration.*) rm -rf -- "$work_dir" ;;
  esac
}
trap cleanup EXIT

for command in docker openssl go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command is unavailable: $command" >&2
    exit 1
  fi
done

mkdir -p "$work_dir/certs" "$work_dir/ldifs" "$work_dir/secrets" "$work_dir/go-cache"
chmod 0700 "$work_dir" "$work_dir/secrets"

admin_password="$(openssl rand -hex 24)"
user_password="$(openssl rand -hex 24)"
printf '%s' "$admin_password" >"$work_dir/secrets/admin-password"
chmod 0600 "$work_dir/secrets/admin-password"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/certs/ca.key" -out "$work_dir/certs/ca.crt" \
  -subj "/CN=Semaphore LDAP Integration CA" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$work_dir/certs/server.key" -out "$work_dir/certs/server.csr" \
  -subj "/CN=localhost" >/dev/null 2>&1
printf '%s\n' \
  'subjectAltName=DNS:localhost,IP:127.0.0.1' \
  'extendedKeyUsage=serverAuth' \
  >"$work_dir/certs/server.ext"
openssl x509 -req -days 1 -sha256 \
  -in "$work_dir/certs/server.csr" \
  -CA "$work_dir/certs/ca.crt" -CAkey "$work_dir/certs/ca.key" -CAcreateserial \
  -extfile "$work_dir/certs/server.ext" -out "$work_dir/certs/server.crt" \
  >/dev/null 2>&1
chmod 0644 "$work_dir/certs/server.key"

{
  printf '%s\n' \
    'dn: dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: dcObject' \
    'objectClass: organization' \
    'dc: example' \
    'o: Semaphore LDAP Integration' \
    '' \
    'dn: ou=people,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: organizationalUnit' \
    'ou: people' \
    '' \
    'dn: ou=duplicate-one,ou=people,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: organizationalUnit' \
    'ou: duplicate-one' \
    '' \
    'dn: ou=duplicate-two,ou=people,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: organizationalUnit' \
    'ou: duplicate-two' \
    '' \
    'dn: uid=alice,ou=people,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: inetOrgPerson' \
    'uid: alice' \
    'cn: Alice Example' \
    'sn: Example' \
    'mail: alice@example.test'
  printf 'userPassword: %s\n\n' "$user_password"
  printf '%s\n' \
    'dn: uid=duplicate-one,ou=duplicate-one,ou=people,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: inetOrgPerson' \
    'uid: duplicate' \
    'cn: Duplicate One' \
    'sn: One' \
    'mail: duplicate-one@example.test'
  printf 'userPassword: %s\n\n' "$user_password"
  printf '%s\n' \
    'dn: uid=duplicate-two,ou=duplicate-two,ou=people,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: inetOrgPerson' \
    'uid: duplicate' \
    'cn: Duplicate Two' \
    'sn: Two' \
    'mail: duplicate-two@example.test'
  printf 'userPassword: %s\n\n' "$user_password"
  printf '%s\n' \
    'dn: ou=external,dc=example,dc=test' \
    'objectClass: top' \
    'objectClass: referral' \
    'objectClass: extensibleObject' \
    'ou: external' \
    'ref: ldaps://untrusted.example.test/dc=outside'
} >"$work_dir/ldifs/10-integration.ldif"

export SEMAPHORE_LDAP_CERT_DIR="$work_dir/certs"
export SEMAPHORE_LDAP_LDIF_DIR="$work_dir/ldifs"
export SEMAPHORE_LDAP_SECRET_DIR="$work_dir/secrets"
export SEMAPHORE_LDAP_ADMIN_PASSWORD="$admin_password"
export SEMAPHORE_LDAP_USER_PASSWORD="$user_password"
export SEMAPHORE_LDAP_CA_FILE="$work_dir/certs/ca.crt"

docker compose --project-name "$compose_project" --file "$script_dir/compose.yaml" up --detach
compose_started=true

ready=false
health_log="$work_dir/ldap-health.log"
for _ in {1..60}; do
  if docker compose --project-name "$compose_project" --file "$script_dir/compose.yaml" \
    exec --no-TTY openldap env LDAPTLS_CACERT=/certs/ca.crt ldapsearch -x \
      -H ldaps://localhost:1636 -D 'cn=admin,dc=example,dc=test' \
      -y /run/secrets/admin-password -b 'dc=example,dc=test' -s base dn \
      >"$health_log" 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  docker compose --project-name "$compose_project" --file "$script_dir/compose.yaml" logs
  sed -n '1,80p' "$health_log" >&2
  echo 'Disposable TLS LDAP server did not become ready.' >&2
  exit 1
fi

published_address="$(docker compose --project-name "$compose_project" \
  --file "$script_dir/compose.yaml" port openldap 1636)"
export SEMAPHORE_LDAP_URL="ldaps://localhost:${published_address##*:}"
export GOCACHE="$work_dir/go-cache"

(cd -- "$repository_dir" && go test -tags=ldap_integration ./services/identity \
  -run TestLDAPTLSIntegration -count=1)
(cd -- "$repository_dir/test/edition-contract/enhanced" && \
  go test -tags=ldap_integration ./pkg/features \
    -run TestLDAPLifecycleTLSOutageAndRecoveryIntegration -count=1)

echo 'Disposable TLS LDAP integration passed.'
