#!/bin/sh
# Run only in minio-init. Secrets enter via environment/config/stdin, never argv.
set -eu
umask 077

: "${MINIO_ROOT_USER:?}" "${MINIO_ROOT_PASSWORD:?}"
: "${BACKUP_S3_ACCESS_KEY:?}" "${BACKUP_S3_SECRET_KEY:?}"
: "${BACKUP_S3_BUCKET:?}" "${BACKUP_S3_PREFIX:?}"
for value in "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" "$BACKUP_S3_ACCESS_KEY" "$BACKUP_S3_SECRET_KEY"; do
    case "$value" in *REPLACE*|*'
'*) echo "Fill dedicated single-line MinIO credentials before setup" >&2; exit 1 ;; esac
done
if [ "$MINIO_ROOT_USER" = "$BACKUP_S3_ACCESS_KEY" ]; then
    echo "Worker credentials must differ from the MinIO administrator" >&2
    exit 1
fi
case "$BACKUP_S3_BUCKET" in ''|*[!a-z0-9.-]*) echo "Invalid dedicated backup bucket" >&2; exit 1 ;; esac
case "$BACKUP_S3_PREFIX" in ''|/*|*/|*..*|*[!a-zA-Z0-9_./-]*) echo "Invalid bounded backup prefix" >&2; exit 1 ;; esac

export MC_CONFIG_DIR=/tmp/mc
mkdir -p "$MC_CONFIG_DIR"
jq -n '{version:"10", aliases:{bootstrap:{url:"http://minio:9000",accessKey:env.MINIO_ROOT_USER,secretKey:env.MINIO_ROOT_PASSWORD,api:"S3v4",path:"auto"},worker:{url:"http://minio:9000",accessKey:env.BACKUP_S3_ACCESS_KEY,secretKey:env.BACKUP_S3_SECRET_KEY,api:"S3v4",path:"auto"}}}' > "$MC_CONFIG_DIR/config.json"

mc mb --ignore-existing "bootstrap/$BACKUP_S3_BUCKET" >/dev/null
mc anonymous set none "bootstrap/$BACKUP_S3_BUCKET" >/dev/null
jq -n '{Version:"2012-10-17",Statement:[{Effect:"Allow",Action:["s3:GetBucketLocation","s3:GetBucketPolicy","s3:ListBucket","s3:ListBucketMultipartUploads"],Resource:[("arn:aws:s3:::"+env.BACKUP_S3_BUCKET)]},{Effect:"Allow",Action:["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:AbortMultipartUpload","s3:ListMultipartUploadParts"],Resource:[("arn:aws:s3:::"+env.BACKUP_S3_BUCKET+"/"+env.BACKUP_S3_PREFIX+"/*")]}]}' > /tmp/worker-policy.json
mc admin policy create bootstrap managed-backup-worker /tmp/worker-policy.json >/dev/null
printf '%s\n%s\n' "$BACKUP_S3_ACCESS_KEY" "$BACKUP_S3_SECRET_KEY" | mc admin user add bootstrap >/dev/null
mc admin policy attach bootstrap managed-backup-worker --user "$BACKUP_S3_ACCESS_KEY" >/dev/null

# Prove worker writes/reads and anonymous denial using one disposable object.
probe_key="$BACKUP_S3_PREFIX/.minio-check-$(cat /proc/sys/kernel/random/uuid)"
cleanup() {
    mc rm "worker/$BACKUP_S3_BUCKET/$probe_key" >/dev/null 2>&1 || true
    rm -rf "$MC_CONFIG_DIR" /tmp/worker-policy.json /tmp/minio-probe /tmp/minio-probe-read
}
trap cleanup EXIT HUP INT TERM
printf 'private backup storage check\n' > /tmp/minio-probe
mc cp /tmp/minio-probe "worker/$BACKUP_S3_BUCKET/$probe_key" >/dev/null
mc cp "worker/$BACKUP_S3_BUCKET/$probe_key" /tmp/minio-probe-read >/dev/null
cmp /tmp/minio-probe /tmp/minio-probe-read
anonymous_status="$(curl --silent --output /dev/null --write-out '%{http_code}' --head "http://minio:9000/$BACKUP_S3_BUCKET/$probe_key")"
case "$anonymous_status" in 401|403) ;; *) echo "Anonymous object access was not denied" >&2; exit 1 ;; esac
mc stat "worker/$BACKUP_S3_BUCKET" >/dev/null
echo "Private bucket and scoped worker account ready; read/write/anonymous-denial checks passed"
