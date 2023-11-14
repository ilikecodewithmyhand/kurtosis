#
# Copyright (c) 2022 - present Kurtosis Technologies Inc.
# All Rights Reserved.
#

set -euo pipefail   # Bash "strict mode"
script_dirpath="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
expander_root_dirpath="$(dirname "${script_dirpath}")"
git_repo_dirpath="$(dirname "$(dirname "${expander_root_dirpath}")")"
uname_arch=$(uname -m)
# ==================================================================================================
#                                             Constants
# ==================================================================================================
source "${script_dirpath}/_constants.env"

BUILD_DIRNAME="build"

DEFAULT_SKIP_DOCKER_IMAGE_BUILDING=false

DEFAULT_ARCHITECTURE_TO_BUILD="amd64"

#if [ "$uname_arch" == "x86_64" ] || [ "$uname_arch" == "amd64" ]; then
#    DEFAULT_ARCHITECTURE_TO_BUILD="amd64"
#elif [ "$uname_arch" == "aarch64" ] || [ "$uname_arch" == "arm64" ]; then
#    DEFAULT_ARCHITECTURE_TO_BUILD="arm64"
#fi

MAIN_GO_FILEPATH="${expander_root_dirpath}/main.go"
MAIN_BINARY_OUTPUT_FILENAME="files-artifacts-expander"
MAIN_BINARY_OUTPUT_FILEPATH="${expander_root_dirpath}/${BUILD_DIRNAME}/${MAIN_BINARY_OUTPUT_FILENAME}"

# =============================================================================
#                                 Main Code
# =============================================================================
skip_docker_image_building="${1:-"${DEFAULT_SKIP_DOCKER_IMAGE_BUILDING}"}"
if [ "${skip_docker_image_building}" != "true" ] && [ "${skip_docker_image_building}" != "false" ]; then
    echo "Error: Invalid skip-docker-image-building arg '${skip_docker_image_building}'" >&2
fi

architecture_to_build="${2:-"${DEFAULT_ARCHITECTURE_TO_BUILD}"}"
if [ "${architecture_to_build}" != "amd64" ] && [ "${architecture_to_build}" != "arm64" ]; then
    echo "Error: Invalid architecture-to-build arg '${architecture_to_build}'" >&2
fi

# Checks if dockerignore file is in the root path
if ! [ -f "${expander_root_dirpath}"/.dockerignore ]; then
  echo "Error: No .dockerignore file found in files artifacts expander root '${expander_root_dirpath}'; this is required so Docker caching is enabled and the image builds remain quick" >&2
  exit 1
fi

# Test code
echo "Running unit tests..."
if ! cd "${expander_root_dirpath}"; then
  echo "Couldn't cd to the files artifacts expander root dirpath '${expander_root_dirpath}'" >&2
  exit 1
fi
if ! CGO_ENABLED=0 go test "./..."; then
  echo "Tests failed!" >&2
  exit 1
fi
echo "Tests succeeded"

# Build binary for packaging inside an Alpine Linux image
echo "Building files artifacts expander main.go '${MAIN_GO_FILEPATH}'..."
if ! CGO_ENABLED=0 GOOS=linux GOARCH=${architecture_to_build} go build -o "${MAIN_BINARY_OUTPUT_FILEPATH}.${architecture_to_build}" "${MAIN_GO_FILEPATH}"; then
  echo "Error: An error occurred building the files artifacts expander code" >&2
  exit 1
fi
echo "Successfully built files artifacts expander code"

# Generate Docker image tag
if ! cd "${git_repo_dirpath}"; then
  echo "Error: Couldn't cd to the git root dirpath '${git_repo_dirpath}'" >&2
  exit 1
fi
if ! docker_tag="$(./scripts/get-docker-tag.sh)"; then
    echo "Error: Couldn't get the Docker image tag" >&2
    exit 1
fi

# Build Docker image if requested
if "${skip_docker_image_building}"; then
  echo "Not building docker image as requested"
  exit 0
fi

dockerfile_filepath="${expander_root_dirpath}/Dockerfile"
image_name="${IMAGE_ORG_AND_REPO}:${docker_tag}"
load_not_push_image=false
docker_build_script_cmd="${git_repo_dirpath}/scripts/docker-image-builder.sh ${load_not_push_image} ${dockerfile_filepath} ${image_name}"
if ! eval "${docker_build_script_cmd}"; then
  echo "Error: Docker build failed" >&2
fi
