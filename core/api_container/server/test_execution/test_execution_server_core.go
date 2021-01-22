/*
 * Copyright (c) 2021 - present Kurtosis Technologies LLC.
 * All Rights Reserved.
 */

package test_execution

import (
	"github.com/kurtosis-tech/kurtosis/api_container/api/bindings"
	"github.com/kurtosis-tech/kurtosis/api_container/exit_codes"
	"github.com/kurtosis-tech/kurtosis/api_container/server"
	"github.com/kurtosis-tech/kurtosis/api_container/test_execution_mode/service_network"
	"github.com/kurtosis-tech/kurtosis/commons/docker_manager"
	"google.golang.org/grpc"
)

type TestExecutionServerCore struct {
	dockerManager *docker_manager.DockerManager
	serviceNetwork *service_network.ServiceNetwork
	testSuiteContainerId string
}

func NewTestExecutionServerCore(dockerManager *docker_manager.DockerManager, serviceNetwork *service_network.ServiceNetwork, testSuiteContainerId string) *TestExecutionServerCore {
	return &TestExecutionServerCore{dockerManager: dockerManager, serviceNetwork: serviceNetwork, testSuiteContainerId: testSuiteContainerId}
}

func (core TestExecutionServerCore) GetSuiteAction() bindings.SuiteAction {
	return bindings.SuiteAction_EXECUTE_TEST
}

func (core TestExecutionServerCore) CreateAndRegisterService(shutdownChan chan exit_codes.ApiContainerExitCode, grpcServer *grpc.Server) server.ApiContainerServerService {
	service := NewTestExecutionService(
		core.dockerManager,
		core.serviceNetwork,
		core.testSuiteContainerId,
		shutdownChan)
	bindings.RegisterTestExecutionServiceServer(grpcServer, service)
	return service
}
