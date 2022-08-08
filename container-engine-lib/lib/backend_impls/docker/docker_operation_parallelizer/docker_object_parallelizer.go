package docker_operation_parallelizer

import (
	"context"
	"github.com/kurtosis-tech/container-engine-lib/lib/backend_impls/docker/docker_manager"
	"github.com/kurtosis-tech/container-engine-lib/lib/operation_parallelizer"
)

// DockerOperation represents an operation done on a Docker object (identified by Docker object ID)
type DockerOperation func(ctx context.Context, dockerManager *docker_manager.DockerManager, dockerObjectId string) error

// RunDockerOperationInParallel will run a Docker operation on each of the object IDs, in parallel
// NOTE: Each call to this will get its own threadpool, so it's possible overwhelm Docker with many calls to this;
// we can fix this if it becomes problematic
func RunDockerOperationInParallel(
	ctx context.Context,
// The IDs of the Docker objects to operate on
	dockerObjectIdSet map[string]bool,
	dockerManager *docker_manager.DockerManager,
	operationToApplyToAllDockerObjects DockerOperation,
) (
	map[string]bool,
	map[string]error,
){
	dockerOperations := map[operation_parallelizer.OperationID]operation_parallelizer.Operation{}

	for dockerObjectId, _ := range dockerObjectIdSet {
		opID := operation_parallelizer.OperationID(dockerObjectId)
		dockerOperations[opID] = func() (interface{}, error) {
			return nil, operationToApplyToAllDockerObjects(ctx, dockerManager, dockerObjectId)
		}
	}

	successfulOps, failedOps := operation_parallelizer.RunOperationsInParallel(dockerOperations)

	success := map[string]bool{}
	failed := map[string]error{}

	for opID, _ := range successfulOps {
		success[string(opID)] = true
	}
	for opID, _ := range failedOps {
		failed[string(opID)] = failedOps[opID]
	}

	return success, failed
}
