package user_service_functions

import (
	"context"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/backend_impls/docker/docker_manager"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/backend_interface/objects/enclave"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/backend_interface/objects/service"
	"github.com/kurtosis-tech/kurtosis/container-engine-lib/lib/free_ip_addr_tracker"
	"github.com/kurtosis-tech/stacktrace"
	"sync"
)

/*
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!! WARNING !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
This code is INCREDIBLY tricky, as a result of:

 1. Needing to do service registrations to get an IP address before the service container is started

 2. Docker not having a canonical way to represent a service registration-before-container-started,
    which requires us to use an in-memory registration map

    Be VERY careful when modifying this code, and ideally get Kevin's eyes on it!!

!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!! WARNING !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
*/
func DestroyUserServices(
	ctx context.Context,
	enclaveId enclave.EnclaveID,
	filters *service.ServiceFilters,
	serviceRegistrationsForEnclave map[service.ServiceGUID]*service.ServiceRegistration,
	serviceRegistrationMutex *sync.Mutex,
	freeIpProviderForEnclave *free_ip_addr_tracker.FreeIpAddrTracker,
	dockerManager *docker_manager.DockerManager,
) (
	resultSuccessfulGuids map[service.ServiceGUID]bool,
	resultErroredGuids map[service.ServiceGUID]error,
	resultErr error,
) {
	// Write lock, because we'll be modifying the service registration info
	serviceRegistrationMutex.Lock()
	defer serviceRegistrationMutex.Unlock()

	successfulGuids, erroredGuids, err := destroyUserServicesUnlocked(ctx, enclaveId, filters, serviceRegistrationsForEnclave, freeIpProviderForEnclave, dockerManager)
	if err != nil {
		return nil, nil, stacktrace.Propagate(err, "An error occurred while destroying user services")
	}

	return successfulGuids, erroredGuids, nil
}
