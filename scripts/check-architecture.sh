#!/usr/bin/env bash
set -euo pipefail

module_path="$(go list -m)"
violations=0

# Surface any compile errors or internal-visibility violations before the
# import analysis below; do not swallow go list failures.
go list -deps ./... >/dev/null

# ---------------------------------------------------------------------------
# Helper: determine the service root for a package path.
#   app/<svc>/...       → service root is micro-one-api/app/<svc>
#   internal/...         → service root is micro-one-api/internal (relay-gateway)
# Returns "" for packages outside any service boundary.
# ---------------------------------------------------------------------------
get_service_root() {
  local pkg="$1"
  case "${pkg}" in
    "${module_path}/app/"*)
      local relative="${pkg#${module_path}/app/}"
      printf '%s/app/%s' "${module_path}" "$(printf '%s' "${relative}" | cut -d/ -f1)"
      ;;
    "${module_path}/internal/"*)
      printf '%s/internal' "${module_path}"
      ;;
    *)
      printf ''
      ;;
  esac
}

# ---------------------------------------------------------------------------
# Helper: extract the layer name from a package path relative to its service
# root. Returns "" for non-layer packages (conf, server, cmd, testutil, etc.).
# Valid layers: biz, data, service
#
# For app services:  relative is "internal/biz" or "internal/biz/sub..."
# For root internal:  relative is "biz" or "biz/sub..."
# ---------------------------------------------------------------------------
get_layer() {
  local pkg="$1"
  local svc_root
  svc_root="$(get_service_root "${pkg}")"
  if [[ -z "${svc_root}" ]]; then
    printf ''
    return
  fi
  local relative="${pkg#${svc_root}/}"
  case "${relative}" in
    internal/biz|internal/biz/*|biz|biz/*) printf 'biz' ;;
    internal/data|internal/data/*|data|data/*) printf 'data' ;;
    internal/service|internal/service/*|service|service/*) printf 'service' ;;
    *) printf '' ;;
  esac
}

# ---------------------------------------------------------------------------
# Main import analysis loop
# ---------------------------------------------------------------------------
while IFS='|' read -r package_path imports; do
  service_root="$(get_service_root "${package_path}")"
  pkg_layer="$(get_layer "${package_path}")"

  for imported in ${imports}; do
    imported_root="$(get_service_root "${imported}")"
    imported_layer="$(get_layer "${imported}")"

    # Rule 1: an app subtree must not import another app subtree's
    # implementation. The root internal/ (relay-gateway) is also treated as
    # an app subtree.
    # Exception: testutil packages are explicitly designed for cross-app
    # test sharing.
    if [[ -n "${service_root}" \
          && ( "${imported}" == "${module_path}/app/"* || "${imported}" == "${module_path}/internal/"* ) \
          && "${imported}" != "${service_root}" \
          && "${imported}" != "${service_root}/"* \
          && "${imported}" != */testutil \
          && "${imported}" != */testutil/* ]]; then
      echo "${package_path} imports another app implementation: ${imported}"
      violations=1
    fi

    # Rule 2: platform/pkg/domain must not reverse-import app or root internal.
    if [[ "${package_path}" =~ ^${module_path}/(platform|pkg|domain)/ \
          && ( "${imported}" == "${module_path}/app/"* || "${imported}" == "${module_path}/internal/"* ) ]]; then
      echo "${package_path} has reverse dependency on app: ${imported}"
      violations=1
    fi

    # Rule 3: pkg must remain a pure utility layer (no platform/domain imports).
    if [[ "${package_path}" == "${module_path}/pkg/"* \
          && "${imported}" =~ ^${module_path}/(platform|domain)/ ]]; then
      echo "${package_path} is not a pure utility package: ${imported}"
      violations=1
    fi

    # Rule 4 (NEW): service layer must not import data layer directly.
    # The service layer should depend on biz (usecases/repo interfaces),
    # never on data (implementation). This enforces the
    # DTO→service→DO→biz→data→PO layering contract.
    if [[ "${pkg_layer}" == "service" \
          && "${imported_layer}" == "data" \
          && "${imported_root}" == "${service_root}" ]]; then
      echo "${package_path} (service layer) imports data layer directly: ${imported}"
      violations=1
    fi

    # Rule 5 (NEW): biz layer must not import service layer.
    # Business logic should never depend on transport/DTO concerns.
    if [[ "${pkg_layer}" == "biz" \
          && "${imported_layer}" == "service" \
          && "${imported_root}" == "${service_root}" ]]; then
      echo "${package_path} (biz layer) imports service layer: ${imported}"
      violations=1
    fi

    # Rule 6 (NEW): biz layer must not import data layer.
    # The biz layer defines Repo interfaces; the data layer implements them.
    # Biz should never depend on data implementation details.
    if [[ "${pkg_layer}" == "biz" \
          && "${imported_layer}" == "data" \
          && "${imported_root}" == "${service_root}" ]]; then
      echo "${package_path} (biz layer) imports data layer: ${imported}"
      violations=1
    fi

    # Rule 7 (NEW): biz layer must not import service-specific API DTO
    # packages (proto-generated). The biz layer should work with domain
    # objects (DOs), not wire DTOs.
    # Exception: api/common/v1 (shared constants/errors only).
    if [[ "${pkg_layer}" == "biz" \
          && "${imported}" =~ ^${module_path}/api/[^/]+/v1$ \
          && "${imported}" != "${module_path}/api/common/v1" ]]; then
      echo "${package_path} (biz layer) imports API DTO package: ${imported}"
      violations=1
    fi

    # Rule 8 (NEW): data layer must not import service layer.
    if [[ "${pkg_layer}" == "data" \
          && "${imported_layer}" == "service" \
          && "${imported_root}" == "${service_root}" ]]; then
      echo "${package_path} (data layer) imports service layer: ${imported}"
      violations=1
    fi

    # Rule 9 (NEW): data layer must not import service-specific API DTO
    # packages (proto-generated) in sub-services where data = database
    # repository. In those services, the data layer should only touch POs
    # (persistence objects / database models).
    #
    # Exception: when the data layer serves as a gateway adapter (wrapping
    # external gRPC clients and implementing biz interfaces), it must import
    # API DTOs to perform DTO↔DO conversion. This applies to:
    #   - relay-gateway's internal/data (aggregates identity/channel/billing/log)
    #   - monitor's internal/data (wraps channel-service client for health probing)
    # In these cases, the data layer is the correct location for DTO imports,
    # not the biz layer.
    if [[ "${pkg_layer}" == "data" \
          && "${imported}" =~ ^${module_path}/api/[^/]+/v1$ \
          && "${imported}" != "${module_path}/api/common/v1" \
          && "${package_path}" != "${module_path}/internal/data" \
          && "${package_path}" != "${module_path}/app/monitor/internal/data" ]]; then
      echo "${package_path} (data layer) imports API DTO package: ${imported}"
      violations=1
    fi

  done
done < <(go list -e -f '{{.ImportPath}}|{{join .Imports " "}}' ./app/... ./internal/... ./platform/... ./pkg/... ./domain/... 2>/dev/null || true)

# ---------------------------------------------------------------------------
# Rule 10 (NEW): Wire injector compile check.
# Verify that wire.go compiles under the wireinject build tag for every
# service that has Wire injectors. This catches the class of bugs where
# wire.go references helpers only visible under !wireinject.
# ---------------------------------------------------------------------------
wire_pkgs=""
for dir in ./cmd/relay-gateway \
           ./app/config/cmd/config \
           ./app/notify/cmd/notify \
           ./app/log/cmd/log \
           ./app/monitor/cmd/monitor \
           ./app/channel/cmd/channel \
           ./app/identity/cmd/identity \
           ./app/billing/cmd/billing \
           ./app/admin/cmd/admin; do
  if [[ -f "${dir}/wire.go" ]]; then
    wire_pkgs="${wire_pkgs} ${dir}"
  fi
done

if [[ -n "${wire_pkgs}" ]]; then
  if ! go test -tags wireinject ${wire_pkgs} >/dev/null 2>&1; then
    echo "Wire injector compile check failed (go test -tags wireinject)"
    echo "  Run: go test -tags wireinject ${wire_pkgs}"
    violations=1
  fi
fi

exit "${violations}"
