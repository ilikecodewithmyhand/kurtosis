package server

import (
	"context"
	"fmt"
	"io"

	"github.com/kurtosis-tech/kurtosis/engine/server/engine/enclave_manager"
	"github.com/kurtosis-tech/kurtosis/engine/server/engine/types"
	"github.com/kurtosis-tech/kurtosis/engine/server/engine/utils"
	"github.com/kurtosis-tech/kurtosis/grpc-file-transfer/golang/grpc_file_streaming"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_http_api_bindings"
	api "github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_http_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
)

type enclaveRuntime struct {
	enclaveManager           *enclave_manager.EnclaveManager
	remoteApiContainerClient map[string]kurtosis_core_rpc_api_bindings.ApiContainerServiceClient
}

func NewEnclaveRuntime(ctx context.Context, manager *enclave_manager.EnclaveManager) (*enclaveRuntime, error) {
	enclaves, err := manager.GetEnclaves(ctx)
	if err != nil {
		return nil, err
	}

	clients := map[string]kurtosis_core_rpc_api_bindings.ApiContainerServiceClient{}
	for uuid, info := range enclaves {
		conn, err := getGrpcClientConn(info)
		if err != nil {
			logrus.Errorf("Failed to establish gRPC connection with enclave manager container %s on %s", uuid, info.ApiContainerInfo)
			return nil, err
		}
		logrus.Debugf("Creating gRPC client to enclave manager container %s on %s", uuid, info.ApiContainerInfo)
		apiContainerClient := kurtosis_core_rpc_api_bindings.NewApiContainerServiceClient(conn)
		clients[uuid] = apiContainerClient
	}

	runtime := enclaveRuntime{
		enclaveManager:           manager,
		remoteApiContainerClient: clients,
	}

	return &runtime, nil
}

// ===============================================================================================================
// ============================= Implementing  StrictServerInterface =============================================
// ===============================================================================================================

// (GET /enclaves/{enclave_identifier}/artifacts)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierArtifacts(ctx context.Context, request api.GetEnclavesEnclaveIdentifierArtifactsRequestObject) (api.GetEnclavesEnclaveIdentifierArtifactsResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)

	artifacts, err := apiContainerClient.ListFilesArtifactNamesAndUuids(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}

	http_artifacts := utils.MapList(
		artifacts.FileNamesAndUuids,
		func(x *kurtosis_core_rpc_api_bindings.FilesArtifactNameAndUuid) kurtosis_core_http_api_bindings.FileArtifactReference {
			return kurtosis_core_http_api_bindings.FileArtifactReference{
				Name: &x.FileName,
				Uuid: &x.FileUuid,
			}
		})

	result := api.ListFilesArtifactNamesAndUuidsResponse{
		FileNamesAndUuids: &http_artifacts,
	}
	return api.GetEnclavesEnclaveIdentifierArtifacts200JSONResponse(result), nil

}

// (POST /enclaves/{enclave_identifier}/artifacts/local-file)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierArtifactsLocalFile(ctx context.Context, request api.PostEnclavesEnclaveIdentifierArtifactsLocalFileRequestObject) (api.PostEnclavesEnclaveIdentifierArtifactsLocalFileResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Uploading file artifact to enclave %s", enclave_identifier)

	uploaded_artifacts := map[string]api.FileArtifactReference{}
	for {
		// Get next part (file) from the the multipart POST request
		part, err := request.Body.NextPart()
		if err == io.EOF {
			break
		}
		filename := part.FileName()

		client, err := apiContainerClient.UploadFilesArtifact(ctx)
		if err != nil {
			return nil, stacktrace.Propagate(err, "Can't start file upload gRPC call with enclave %s", enclave_identifier)
		}
		clientStream := grpc_file_streaming.NewClientStream[kurtosis_core_rpc_api_bindings.StreamedDataChunk, kurtosis_core_rpc_api_bindings.UploadFilesArtifactResponse](client)

		response, err := clientStream.SendData(
			filename,
			part,
			0, // Length unknown head of time
			func(previousChunkHash string, contentChunk []byte) (*kurtosis_core_rpc_api_bindings.StreamedDataChunk, error) {
				return &kurtosis_core_rpc_api_bindings.StreamedDataChunk{
					Data:              contentChunk,
					PreviousChunkHash: previousChunkHash,
					Metadata: &kurtosis_core_rpc_api_bindings.DataChunkMetadata{
						Name: filename,
					},
				}, nil
			},
		)

		// The response is nil when a file artifact with the same has already been uploaded
		// TODO (edgar) Is this the expected behavior? If so, we should be explicit about it.
		if response != nil {
			artifact_response := api.FileArtifactReference{
				Name: &response.Name,
				Uuid: &response.Uuid,
			}
			uploaded_artifacts[filename] = artifact_response
		}
	}

	return api.PostEnclavesEnclaveIdentifierArtifactsLocalFile200JSONResponse(uploaded_artifacts), nil
}

// (PUT /enclaves/{enclave_identifier}/artifacts/remote-file)
func (manager *enclaveRuntime) PutEnclavesEnclaveIdentifierArtifactsRemoteFile(ctx context.Context, request api.PutEnclavesEnclaveIdentifierArtifactsRemoteFileRequestObject) (api.PutEnclavesEnclaveIdentifierArtifactsRemoteFileResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Uploading file artifact to enclave %s", enclave_identifier)

	storeWebFilesArtifactArgs := kurtosis_core_rpc_api_bindings.StoreWebFilesArtifactArgs{
		Url:  request.Body.Url,
		Name: request.Body.Name,
	}
	stored_artifact, err := apiContainerClient.StoreWebFilesArtifact(ctx, &storeWebFilesArtifactArgs)
	if err != nil {
		logrus.Errorf("Can't start file upload gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't start file upload gRPC call with enclave %s", enclave_identifier)
	}

	artifact_response := api.FileArtifactReference{
		Uuid: &stored_artifact.Uuid,
		Name: &request.Body.Name,
	}
	return api.PutEnclavesEnclaveIdentifierArtifactsRemoteFile200JSONResponse(artifact_response), nil
}

// (PUT /enclaves/{enclave_identifier}/artifacts/services/{service_identifier})
func (manager *enclaveRuntime) PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifier(ctx context.Context, request api.PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifierRequestObject) (api.PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifierResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	service_identifier := request.ServiceIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Storing file artifact from service %s on enclave %s", service_identifier, enclave_identifier)

	storeWebFilesArtifactArgs := kurtosis_core_rpc_api_bindings.StoreFilesArtifactFromServiceArgs{
		ServiceIdentifier: service_identifier,
		SourcePath:        request.Body.SourcePath,
		Name:              request.Body.Name,
	}
	stored_artifact, err := apiContainerClient.StoreFilesArtifactFromService(ctx, &storeWebFilesArtifactArgs)
	if err != nil {
		logrus.Errorf("Can't start file upload gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't start file upload gRPC call with enclave %s", enclave_identifier)
	}

	artifact_response := api.FileArtifactReference{
		Uuid: &stored_artifact.Uuid,
		Name: &request.Body.Name,
	}
	return api.PutEnclavesEnclaveIdentifierArtifactsServicesServiceIdentifier200JSONResponse(artifact_response), nil
}

// (GET /enclaves/{enclave_identifier}/artifacts/{artifact_identifier})
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifier(ctx context.Context, request api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierRequestObject) (api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	artifact_identifier := request.ArtifactIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Inspecting file artifact %s on enclave %s", artifact_identifier, enclave_identifier)

	inspectFilesArtifactContentsRequest := kurtosis_core_rpc_api_bindings.InspectFilesArtifactContentsRequest{
		FileNamesAndUuid: &kurtosis_core_rpc_api_bindings.FilesArtifactNameAndUuid{
			FileName: artifact_identifier,
			FileUuid: artifact_identifier,
		},
	}
	stored_artifact, err := apiContainerClient.InspectFilesArtifactContents(ctx, &inspectFilesArtifactContentsRequest)
	if err != nil {
		logrus.Errorf("Can't inspect artifact using gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't inspect artifact using gRPC call with enclave %s", enclave_identifier)
	}

	artifact_content_list := utils.MapList(
		stored_artifact.FileDescriptions,
		func(x *kurtosis_core_rpc_api_bindings.FileArtifactContentsFileDescription) api.FileArtifactContentsFileDescription {
			size := int64(x.Size)
			return api.FileArtifactContentsFileDescription{
				Path:        &x.Path,
				Size:        &size,
				TextPreview: x.TextPreview,
			}
		})

	artifact_response := api.InspectFilesArtifactContentsResponse{
		FileDescriptions: &artifact_content_list,
	}

	return api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifier200JSONResponse(artifact_response), nil
}

// (GET /enclaves/{enclave_identifier}/artifacts/{artifact_identifier}/download)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownload(ctx context.Context, request api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownloadRequestObject) (api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownloadResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	artifact_identifier := request.ArtifactIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Downloading file artifact %s from enclave %s", artifact_identifier, enclave_identifier)

	downloadFilesArtifactArgs := kurtosis_core_rpc_api_bindings.DownloadFilesArtifactArgs{
		Identifier: artifact_identifier,
	}
	client, err := apiContainerClient.DownloadFilesArtifact(ctx, &downloadFilesArtifactArgs)
	if err != nil {
		logrus.Errorf("Can't start file download gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't start file download gRPC call with enclave %s", enclave_identifier)
	}

	clientStream := grpc_file_streaming.NewClientStream[kurtosis_core_rpc_api_bindings.StreamedDataChunk, []byte](client)
	pipeReader := clientStream.PipeReader(
		artifact_identifier,
		func(dataChunk *kurtosis_core_rpc_api_bindings.StreamedDataChunk) ([]byte, string, error) {
			return dataChunk.Data, dataChunk.PreviousChunkHash, nil
		},
	)

	response := api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownload200ApplicationoctetStreamResponse{
		Body:          pipeReader,
		ContentLength: 0, // No file size is provided since we are streaming it directly
	}

	return api.GetEnclavesEnclaveIdentifierArtifactsArtifactIdentifierDownload200ApplicationoctetStreamResponse(response), nil
}

// (GET /enclaves/{enclave_identifier}/services)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierServices(ctx context.Context, request api.GetEnclavesEnclaveIdentifierServicesRequestObject) (api.GetEnclavesEnclaveIdentifierServicesResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Getting info about services enclave %s", enclave_identifier)

	service_ids := utils.DerefWith(request.Params.Services, []string{})
	getServicesArgs := kurtosis_core_rpc_api_bindings.GetServicesArgs{
		ServiceIdentifiers: utils.NewMapFromList(service_ids, func(x string) bool { return true }),
	}
	services, err := apiContainerClient.GetServices(ctx, &getServicesArgs)
	if err != nil {
		logrus.Errorf("Can't list services using gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't  list services using gRPC call with enclave %s", enclave_identifier)
	}

	mapped_services := utils.MapMapValues(services.ServiceInfo, toHttpServiceInfo)
	return api.GetEnclavesEnclaveIdentifierServices200JSONResponse(mapped_services), nil
}

// (GET /enclaves/{enclave_identifier}/services/history)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierServicesHistory(ctx context.Context, request api.GetEnclavesEnclaveIdentifierServicesHistoryRequestObject) (api.GetEnclavesEnclaveIdentifierServicesHistoryResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Listing services from enclave %s", enclave_identifier)

	services, err := apiContainerClient.GetExistingAndHistoricalServiceIdentifiers(ctx, &emptypb.Empty{})
	if err != nil {
		logrus.Errorf("Can't list services using gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't  list services using gRPC call with enclave %s", enclave_identifier)
	}

	response := utils.MapList(services.AllIdentifiers, func(service *kurtosis_core_rpc_api_bindings.ServiceIdentifiers) api.ServiceIdentifiers {
		return api.ServiceIdentifiers{
			ServiceUuid:   &service.ServiceUuid,
			ShortenedUuid: &service.ShortenedUuid,
			Name:          &service.Name,
		}
	})

	return api.GetEnclavesEnclaveIdentifierServicesHistory200JSONResponse(response), nil
}

// (POST /enclaves/{enclave_identifier}/services/connection)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierServicesConnection(ctx context.Context, request api.PostEnclavesEnclaveIdentifierServicesConnectionRequestObject) (api.PostEnclavesEnclaveIdentifierServicesConnectionResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Listing services from enclave %s", enclave_identifier)

	connectServicesArgs := kurtosis_core_rpc_api_bindings.ConnectServicesArgs{
		Connect: toGrpcConnect(*request.Body.Connect),
	}
	_, err := apiContainerClient.ConnectServices(ctx, &connectServicesArgs)
	if err != nil {
		logrus.Errorf("Can't list services using gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't  list services using gRPC call with enclave %s", enclave_identifier)
	}

	return api.PostEnclavesEnclaveIdentifierServicesConnection200JSONResponse{}, nil
}

// (GET /enclaves/{enclave_identifier}/services/{service_identifier})
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierServicesServiceIdentifier(ctx context.Context, request api.GetEnclavesEnclaveIdentifierServicesServiceIdentifierRequestObject) (api.GetEnclavesEnclaveIdentifierServicesServiceIdentifierResponseObject, error) {
	enclave_identifier := request.EnclaveIdentifier
	service_identifier := request.ServiceIdentifier
	apiContainerClient := manager.GetGrpcClientForEnclaveUUID(enclave_identifier)
	logrus.Infof("Getting info about service %s from enclave %s", service_identifier, enclave_identifier)

	getServicesArgs := kurtosis_core_rpc_api_bindings.GetServicesArgs{
		ServiceIdentifiers: map[string]bool{service_identifier: true},
	}
	services, err := apiContainerClient.GetServices(ctx, &getServicesArgs)
	if err != nil {
		logrus.Errorf("Can't list services using gRPC call with enclave %s, error: %s", enclave_identifier, err)
		return nil, stacktrace.NewError("Can't  list services using gRPC call with enclave %s", enclave_identifier)
	}

	mapped_services := utils.MapMapValues(services.ServiceInfo, toHttpServiceInfo)
	selected_service, found := mapped_services[service_identifier]
	if !found {
		// TODO(edgar) add 404 return
		return nil, stacktrace.NewError("Service %s not found", service_identifier)
	}
	return api.GetEnclavesEnclaveIdentifierServicesServiceIdentifier200JSONResponse(selected_service), nil
}

// (POST /enclaves/{enclave_identifier}/services/{service_identifier}/command)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierServicesServiceIdentifierCommand(ctx context.Context, request api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierCommandRequestObject) (api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierCommandResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/services/{service_identifier}/endpoints/{port_number}/availability)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierServicesServiceIdentifierEndpointsPortNumberAvailability(ctx context.Context, request api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierEndpointsPortNumberAvailabilityRequestObject) (api.PostEnclavesEnclaveIdentifierServicesServiceIdentifierEndpointsPortNumberAvailabilityResponseObject, error) {
	return nil, Error{}
}

// (GET /enclaves/{enclave_identifier}/starlark)
func (manager *enclaveRuntime) GetEnclavesEnclaveIdentifierStarlark(ctx context.Context, request api.GetEnclavesEnclaveIdentifierStarlarkRequestObject) (api.GetEnclavesEnclaveIdentifierStarlarkResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/starlark/packages)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierStarlarkPackages(ctx context.Context, request api.PostEnclavesEnclaveIdentifierStarlarkPackagesRequestObject) (api.PostEnclavesEnclaveIdentifierStarlarkPackagesResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/starlark/packages/{package_id})
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierStarlarkPackagesPackageId(ctx context.Context, request api.PostEnclavesEnclaveIdentifierStarlarkPackagesPackageIdRequestObject) (api.PostEnclavesEnclaveIdentifierStarlarkPackagesPackageIdResponseObject, error) {
	return nil, Error{}
}

// (POST /enclaves/{enclave_identifier}/starlark/scripts)
func (manager *enclaveRuntime) PostEnclavesEnclaveIdentifierStarlarkScripts(ctx context.Context, request api.PostEnclavesEnclaveIdentifierStarlarkScriptsRequestObject) (api.PostEnclavesEnclaveIdentifierStarlarkScriptsResponseObject, error) {
	return nil, Error{}
}

// ===============================================================================================================
// ===============================================================================================================
// ===============================================================================================================

// GetGrpcClientConn returns a client conn dialed in to the local port
// It is the caller's responsibility to call resultClientConn.close()
func getGrpcClientConn(enclaveInfo *types.EnclaveInfo) (resultClientConn *grpc.ClientConn, resultErr error) {
	// apiContainerGrpcPort := enclaveInfo.ApiContainerInfo.GrpcPortInsideEnclave
	// apiContainerIP := enclaveInfo.ApiContainerInfo.ContainerId
	apiContainerGrpcPort := enclaveInfo.ApiContainerHostMachineInfo.GrpcPortOnHostMachine
	apiContainerIP := enclaveInfo.ApiContainerHostMachineInfo.IpOnHostMachine
	grpcServerAddress := fmt.Sprintf("%v:%v", apiContainerIP, apiContainerGrpcPort)
	grpcConnection, err := grpc.Dial(grpcServerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, stacktrace.Propagate(err, "Expected to be able to create a GRPC client connection on address '%v', but a non-nil error was returned", grpcServerAddress)
	}
	return grpcConnection, nil
}

func (manager enclaveRuntime) GetGrpcClientForEnclaveUUID(enclave_uuid string) kurtosis_core_rpc_api_bindings.ApiContainerServiceClient {
	client, found := manager.remoteApiContainerClient[enclave_uuid]
	if !found {
		// TODO(edgar): add logic to retry/refresh map
		panic(fmt.Sprintf("can't find enclave %s", enclave_uuid))
	}
	return client
}

func toGrpcConnect(conn api.Connect) kurtosis_core_rpc_api_bindings.Connect {
	switch conn {
	case api.CONNECT:
		return kurtosis_core_rpc_api_bindings.Connect_CONNECT
	case api.NOCONNECT:
		return kurtosis_core_rpc_api_bindings.Connect_NO_CONNECT
	default:
		panic(fmt.Sprintf("Missing conversion of Connect Enum value: %s", conn))
	}
}

func toHttpContainerStatus(status kurtosis_core_rpc_api_bindings.Container_Status) api.ContainerStatus {
	switch status {
	case kurtosis_core_rpc_api_bindings.Container_RUNNING:
		return api.ContainerStatusRUNNING
	case kurtosis_core_rpc_api_bindings.Container_STOPPED:
		return api.ContainerStatusSTOPPED
	case kurtosis_core_rpc_api_bindings.Container_UNKNOWN:
		return api.ContainerStatusUNKNOWN
	default:
		panic(fmt.Sprintf("Missing conversion of Container Status Enum value: %s", status))
	}
}

func toHttpTransportProtocol(protocol kurtosis_core_rpc_api_bindings.Port_TransportProtocol) api.PortTransportProtocol {
	switch protocol {
	case kurtosis_core_rpc_api_bindings.Port_TCP:
		return api.TCP
	case kurtosis_core_rpc_api_bindings.Port_UDP:
		return api.UDP
	case kurtosis_core_rpc_api_bindings.Port_SCTP:
		return api.SCTP
	default:
		panic(fmt.Sprintf("Missing conversion of Transport Protocol Enum value: %s", protocol))
	}
}

func toHttpServiceStatus(status kurtosis_core_rpc_api_bindings.ServiceStatus) api.ServiceStatus {
	switch status {
	case kurtosis_core_rpc_api_bindings.ServiceStatus_RUNNING:
		return api.ServiceStatusRUNNING
	case kurtosis_core_rpc_api_bindings.ServiceStatus_STOPPED:
		return api.ServiceStatusSTOPPED
	case kurtosis_core_rpc_api_bindings.ServiceStatus_UNKNOWN:
		return api.ServiceStatusUNKNOWN
	default:
		panic(fmt.Sprintf("Missing conversion of Service Status Enum value: %s", status))
	}
}

func toHttpContainer(container *kurtosis_core_rpc_api_bindings.Container) api.Container {
	status := toHttpContainerStatus(container.Status)
	return api.Container{
		CmdArgs:        &container.CmdArgs,
		EntrypointArgs: &container.EntrypointArgs,
		EnvVars:        &container.EnvVars,
		ImageName:      &container.ImageName,
		Status:         &status,
	}
}

func toHttpPorts(port *kurtosis_core_rpc_api_bindings.Port) api.Port {
	protocol := toHttpTransportProtocol(port.TransportProtocol)
	return api.Port{
		MaybeApplicationProtocol: &port.MaybeApplicationProtocol,
		MaybeWaitTimeout:         &port.MaybeWaitTimeout,
		Number:                   int32(port.Number),
		TransportProtocol:        protocol,
	}
}

func toHttpServiceInfo(service *kurtosis_core_rpc_api_bindings.ServiceInfo) api.ServiceInfo {
	container := toHttpContainer(service.Container)
	serviceStatus := toHttpServiceStatus(service.ServiceStatus)
	publicPorts := utils.MapMapValues(service.MaybePublicPorts, toHttpPorts)
	privatePorts := utils.MapMapValues(service.PrivatePorts, toHttpPorts)
	return api.ServiceInfo{
		Container:         &container,
		MaybePublicIpAddr: &service.MaybePublicIpAddr,
		MaybePublicPorts:  &publicPorts,
		Name:              &service.Name,
		PrivateIpAddr:     &service.PrivateIpAddr,
		PrivatePorts:      &privatePorts,
		ServiceStatus:     &serviceStatus,
		ServiceUuid:       &service.ServiceUuid,
		ShortenedUuid:     &service.ShortenedUuid,
	}
}
