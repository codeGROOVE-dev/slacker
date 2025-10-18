#!/bin/bash
# Deploy the current Go app to Google Cloud run
#
# usage:
#   ./hacks/deploy.sh - deploy the app in the current directory
#   ./hacks/deploy.sh cmd/server - deploy the app in cmd/server
#   APP_NAME=custom-name ./hacks/deploy.sh cmd/server - override app name
set -eux -o pipefail

TARGET_DIR="${1:-.}"

if [ -n "${1:-}" ]; then
    pushd "$1"
fi

PROJECT=${GCP_PROJECT:=chat-bot-army}
REGISTRY="${PROJECT}"
REGION="us-central1"

# Determine app name: use APP_NAME env var if set, otherwise derive from target directory
if [ -z "${APP_NAME:-}" ]; then
    # Get the last component of the target directory path
    # cmd/server -> server, cmd/slack-registrar -> slack-registrar
    APP_NAME=$(basename "$TARGET_DIR")

    # If we're in root directory, fall back to module name
    if [ "$APP_NAME" = "." ]; then
        APP_NAME=$(basename "$(go mod graph | head -n 1 | cut -d" " -f1)")
    fi
fi

APP_USER="${APP_NAME}@${PROJECT}.iam.gserviceaccount.com"
APP_IMAGE="gcr.io/${REGISTRY}/${APP_NAME}"

gcloud iam service-accounts list --project "${PROJECT}" | grep -q "${APP_USER}" ||
	gcloud iam service-accounts create "${APP_NAME}" --project "${PROJECT}"

echo "Deploying app: ${APP_NAME}"
echo "Service account: ${APP_USER}"
echo "Image: ${APP_IMAGE}"

export KO_DOCKER_REPO="${APP_IMAGE}"
gcloud run deploy "${APP_NAME}" \
	--image="$(ko publish .)" \
	--region="${REGION}" \
	--service-account="${APP_USER}" \
	--project "${PROJECT}" \
	--allow-unauthenticated
