package startosis_engine

import (
	"fmt"
	"github.com/go-yaml/yaml"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/service_network"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/instructions_plan"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/add_service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/exec"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/remove_service"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/render_templates"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/store_service_files"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/tasks"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_instruction/upload_files"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_starlark_framework/builtin_argument"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_starlark_framework/kurtosis_type_constructor"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_types"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/kurtosis_types/service_config"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/recipe"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/startosis_errors"
	"github.com/kurtosis-tech/kurtosis/core/server/api_container/server/startosis_engine/startosis_packages"
	"github.com/kurtosis-tech/stacktrace"
	"github.com/sirupsen/logrus"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"strconv"
	"strings"
)

// We need the package id and the args, the args need to be filled in
// the instructions likely come with the args filled in already, but what if no args are passed in? are they left as variables?

// How to represent dependencies within the yaml???
// say a service config refers to another files artifact

// some conversions are:
// add_service -> use service config and returned info to create a ServiceObject
// remove_service -> remove that from the plan representation
// upload_files -> FilesArtifact
// render_template -> FilesArtifact
// run_sh -> Task but returns a files artifact so create that
// run_python -> Task but returns a files artifact so create that
//
// go through all the kurtosis builtins and figure out which ones we need to accommodate for and which ones we don't need to accommodate for

// TODO: refactor this so plan yaml is generated as instructionsPlan is created, otherwise a lot of duplicate operations happen to parse the arguments

// PlanYamlGenerator generates a yaml representation of a [plan].
type PlanYamlGenerator interface {
	// GenerateYaml converts [plan] into a byte array that represents a yaml with information in the plan.
	// The format of the yaml in the byte array is as such:
	//
	//
	//
	//packageId: github.com/kurtosis-tech/postgres-package
	//
	//services:
	//	- uuid:
	//	- name:
	//   service_config:
	//	  	image:
	//		env_var:
	//		...
	//
	//
	//files_artifacts:
	//
	//
	//
	//
	//
	//
	//tasks:
	//
	//

	GenerateYaml(plan instructions_plan.InstructionsPlan) ([]byte, error)
}

type PlanYamlGeneratorImpl struct {
	// Plan generated by an interpretation of a Starlark script of package
	plan *instructions_plan.InstructionsPlan

	serviceNetwork service_network.ServiceNetwork

	packageContentProvider startosis_packages.PackageContentProvider

	packageReplaceOptions map[string]string

	// technically files artifacts are future references but we store them separately bc they are easily identifiable
	// and have a distinct structure (FilesArtifact)
	filesArtifactIndex map[string]*FilesArtifact

	// Store service index needed to see in case a service is referenced by a remove service, or store service later in the plan
	serviceIndex map[string]*Service

	// TODO: do we need a task index?
	taskIndex map[string]*Task

	// Representation of plan in yaml the plan is being processed, the yaml gets updated
	planYaml *PlanYaml

	uuidGenerator int

	futureReferenceIndex map[string]string
}

func NewPlanYamlGenerator(
	plan *instructions_plan.InstructionsPlan,
	serviceNetwork service_network.ServiceNetwork,
	packageId string,
	packageContentProvider startosis_packages.PackageContentProvider,
	packageReplaceOptions map[string]string) *PlanYamlGeneratorImpl {
	return &PlanYamlGeneratorImpl{
		plan:                   plan,
		serviceNetwork:         serviceNetwork,
		packageContentProvider: packageContentProvider,
		packageReplaceOptions:  packageReplaceOptions,
		planYaml: &PlanYaml{
			PackageId:      packageId,
			Services:       []*Service{},
			FilesArtifacts: []*FilesArtifact{},
			Tasks:          []*Task{},
		},
		filesArtifactIndex:   map[string]*FilesArtifact{},
		serviceIndex:         map[string]*Service{},
		taskIndex:            map[string]*Task{},
		uuidGenerator:        0,
		futureReferenceIndex: map[string]string{},
	}
}

func (pyg *PlanYamlGeneratorImpl) GenerateYaml() ([]byte, error) {
	instructionsSequence, err := pyg.plan.GeneratePlan()
	if err != nil {
		return nil, err
	}

	// iterate over the sequence of instructions
	for _, scheduledInstruction := range instructionsSequence {
		var err error
		// based on the instruction, update the plan yaml representation accordingly
		switch getBuiltinNameFromInstruction(scheduledInstruction) {
		case add_service.AddServiceBuiltinName:
			err = pyg.updatePlanYamlFromAddService(scheduledInstruction)
		case remove_service.RemoveServiceBuiltinName:
			err = pyg.updatePlanYamlFromRemoveService(scheduledInstruction)
		case tasks.RunShBuiltinName:
			err = pyg.updatePlanYamlFromRunSh(scheduledInstruction)
		case tasks.RunPythonBuiltinName:
			err = pyg.updatePlanYamlFromRunPython(scheduledInstruction)
		case render_templates.RenderTemplatesBuiltinName:
			err = pyg.updatePlanYamlFromRenderTemplates(scheduledInstruction)
		case upload_files.UploadFilesBuiltinName:
			err = pyg.updatePlanYamlFromUploadFiles(scheduledInstruction)
		case store_service_files.StoreServiceFilesBuiltinName:
			err = pyg.updatePlanYamlFromStoreServiceFiles(scheduledInstruction)
		case exec.ExecBuiltinName:
			err = pyg.updatePlanYamlFromExec(scheduledInstruction)
		default:
			// skip if this instruction is not one that will update the plan yaml
			continue
		}
		if err != nil {
			return nil, err
		}
	}

	// at the very end, convert the plan yaml representation into a yaml
	logrus.Infof("FUTURE REFERENCE INDEX: %v", pyg.futureReferenceIndex)
	return convertPlanYamlToYaml(pyg.planYaml)
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromAddService(addServiceInstruction *instructions_plan.ScheduledInstruction) error { // for type safety, it would be great to be more specific than scheduled instruction
	kurtosisInstruction := addServiceInstruction.GetInstruction()
	arguments := kurtosisInstruction.GetArguments()

	// start building Service Yaml object
	service := &Service{} //nolint:exhaustruct
	uuid := pyg.generateUuid()
	service.Uuid = strconv.Itoa(uuid)

	// store future references of this service
	returnValue := addServiceInstruction.GetReturnedValue()
	returnedService, ok := returnValue.(*kurtosis_types.Service)
	if !ok {
		return stacktrace.NewError("Cast to service didn't work")
	}
	futureRefIPAddress, err := returnedService.GetIpAddress()
	if err != nil {
		return err
	}
	pyg.futureReferenceIndex[futureRefIPAddress] = fmt.Sprintf("{{ kurtosis.%v.ip_address }}", uuid)
	futureRefHostName, err := returnedService.GetHostname()
	if err != nil {
		return err
	}
	pyg.futureReferenceIndex[futureRefHostName] = fmt.Sprintf("{{ kurtosis.%v.hostname }}", uuid)

	var regErr error
	serviceName, regErr := builtin_argument.ExtractArgumentValue[starlark.String](arguments, add_service.ServiceNameArgName)
	if regErr != nil {
		return startosis_errors.WrapWithInterpretationError(regErr, "Unable to extract value for '%s' argument", add_service.ServiceNameArgName)
	}
	service.Name = pyg.swapFutureReference(serviceName.GoString()) // swap future references in the strings

	starlarkServiceConfig, regErr := builtin_argument.ExtractArgumentValue[*service_config.ServiceConfig](arguments, add_service.ServiceConfigArgName)
	if regErr != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", add_service.ServiceConfigArgName)
	}
	serviceConfig, serviceConfigErr := starlarkServiceConfig.ToKurtosisType( // is this an expensive call? // TODO: add this error back in
		pyg.serviceNetwork,
		kurtosisInstruction.GetPositionInOriginalScript().GetFilename(),
		pyg.planYaml.PackageId,
		pyg.packageContentProvider,
		pyg.packageReplaceOptions)
	if serviceConfigErr != nil {
		return serviceConfigErr
	}

	// get image info
	rawImageAttrValue, _, interpretationErr := kurtosis_type_constructor.ExtractAttrValue[starlark.Value](starlarkServiceConfig.KurtosisValueTypeDefault, service_config.ImageAttr)
	if interpretationErr != nil {
		return interpretationErr
	}
	image := &ImageSpec{ //nolint:exhaustruct
		ImageName: serviceConfig.GetContainerImageName(),
	}
	imageBuildSpec := serviceConfig.GetImageBuildSpec()
	if imageBuildSpec != nil {
		switch img := rawImageAttrValue.(type) {
		case *service_config.ImageBuildSpec:
			contextLocator, err := img.GetBuildContextLocator()
			if err != nil {
				return err
			}
			image.BuildContextLocator = contextLocator
		}
		image.TargetStage = imageBuildSpec.GetTargetStage()
	}
	imageSpec := serviceConfig.GetImageRegistrySpec()
	if imageSpec != nil {
		image.Registry = imageSpec.GetRegistryAddr()
	}
	service.Image = image

	// detect future references
	cmdArgs := []string{}
	for _, cmdArg := range serviceConfig.GetCmdArgs() {
		realCmdArg := pyg.swapFutureReference(cmdArg)
		cmdArgs = append(cmdArgs, realCmdArg)
	}
	service.Cmd = cmdArgs

	entryArgs := []string{}
	for _, entryArg := range serviceConfig.GetEntrypointArgs() {
		realEntryArg := pyg.swapFutureReference(entryArg)
		entryArgs = append(entryArgs, realEntryArg)
	}
	service.Entrypoint = entryArgs

	// ports
	service.Ports = []*Port{}
	for portName, configPort := range serviceConfig.GetPrivatePorts() { // TODO: support public ports

		port := &Port{ //nolint:exhaustruct
			TransportProtocol: TransportProtocol(configPort.GetTransportProtocol().String()),
			Name:              portName,
			Number:            configPort.GetNumber(),
		}
		if configPort.GetMaybeApplicationProtocol() != nil {
			port.ApplicationProtocol = ApplicationProtocol(*configPort.GetMaybeApplicationProtocol())
		}

		service.Ports = append(service.Ports, port)
	}

	// env vars
	service.EnvVars = []*EnvironmentVariable{}
	for key, val := range serviceConfig.GetEnvVars() {
		// detect and future references
		value := pyg.swapFutureReference(val)
		envVar := &EnvironmentVariable{
			Key:   key,
			Value: value,
		}
		service.EnvVars = append(service.EnvVars, envVar)
	}

	// file mounts have two cases:
	// 1. the referenced files artifact already exists in the plan, in which case add the referenced files artifact
	// 2. the referenced files artifact does not already exist in the plan, in which case the file MUST have been passed in via a top level arg OR is invalid
	// 	  in this case,
	// 	  - create new files artifact
	//	  - add it to the service's file mount accordingly
	//	  - add the files artifact to the plan
	service.Files = []*FileMount{}
	serviceFilesArtifactExpansions := serviceConfig.GetFilesArtifactsExpansion()
	if serviceFilesArtifactExpansions != nil {
		for mountPath, artifactIdentifiers := range serviceFilesArtifactExpansions.ServiceDirpathsToArtifactIdentifiers {
			fileMount := &FileMount{ //nolint:exhaustruct
				MountPath: mountPath,
			}

			var serviceFilesArtifacts []*FilesArtifact
			for _, identifier := range artifactIdentifiers {
				var filesArtifact *FilesArtifact
				// if there's already a files artifact that exists with this name from a previous instruction, reference that
				if potentialFilesArtifact, ok := pyg.filesArtifactIndex[identifier]; ok {
					filesArtifact = &FilesArtifact{ //nolint:exhaustruct
						Name: potentialFilesArtifact.Name,
						Uuid: potentialFilesArtifact.Uuid,
					}
				} else {
					// otherwise create a new one
					// the only information we have about a files artifact that didn't already exist is the name
					// if it didn't already exist AND interpretation was successful, it MUST HAVE been passed in via args
					filesArtifact = &FilesArtifact{ //nolint:exhaustruct
						Name: identifier,
						Uuid: strconv.Itoa(pyg.generateUuid()),
					}
					pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, filesArtifact)
					pyg.filesArtifactIndex[identifier] = filesArtifact
				}
				serviceFilesArtifacts = append(serviceFilesArtifacts, filesArtifact)
			}

			fileMount.FilesArtifacts = serviceFilesArtifacts
			service.Files = append(service.Files, fileMount)
		}

	}

	pyg.planYaml.Services = append(pyg.planYaml.Services, service)
	pyg.serviceIndex[service.Name] = service
	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromUploadFiles(uploadFilesInstruction *instructions_plan.ScheduledInstruction) error {
	var filesArtifact *FilesArtifact

	// get the name of returned files artifact
	filesArtifactName, castErr := kurtosis_types.SafeCastToString(uploadFilesInstruction.GetReturnedValue(), "files artifact name")
	if castErr != nil {
		return castErr
	}
	filesArtifact = &FilesArtifact{ //nolint:exhaustruct
		Name: filesArtifactName,
		Uuid: strconv.Itoa(pyg.generateUuid()),
	}

	// get files of returned files artifact off render templates config
	arguments := uploadFilesInstruction.GetInstruction().GetArguments()
	src, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, upload_files.SrcArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", upload_files.SrcArgName)
	}
	filesArtifact.Files = []string{src.GoString()}

	// add the files artifact to the yaml and index
	pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, filesArtifact)
	pyg.filesArtifactIndex[filesArtifactName] = filesArtifact
	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromRenderTemplates(renderTemplatesInstruction *instructions_plan.ScheduledInstruction) error {
	var filesArtifact *FilesArtifact

	// get the name of returned files artifact
	filesArtifactName, castErr := kurtosis_types.SafeCastToString(renderTemplatesInstruction.GetReturnedValue(), "files artifact name")
	if castErr != nil {
		return castErr
	}
	filesArtifact = &FilesArtifact{ //nolint:exhaustruct
		Uuid: strconv.Itoa(pyg.generateUuid()),
		Name: filesArtifactName,
	}

	// get files of returned files artifact off render templates config
	arguments := renderTemplatesInstruction.GetInstruction().GetArguments()
	renderTemplateConfig, err := builtin_argument.ExtractArgumentValue[*starlark.Dict](arguments, render_templates.TemplateAndDataByDestinationRelFilepathArg)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to parse '%s'", render_templates.TemplateAndDataByDestinationRelFilepathArg)
	}
	files := []string{}
	for _, filepath := range renderTemplateConfig.Keys() {
		filepathStr, castErr := kurtosis_types.SafeCastToString(filepath, "filepath")
		if castErr != nil {
			return castErr
		}
		files = append(files, filepathStr)
	}
	filesArtifact.Files = files

	// add the files artifact to the yaml and index
	pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, filesArtifact)
	pyg.filesArtifactIndex[filesArtifactName] = filesArtifact

	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromRunSh(runShInstruction *instructions_plan.ScheduledInstruction) error {
	var task *Task

	// store run sh future references
	returnValue := runShInstruction.GetReturnedValue()
	_, ok := returnValue.(*starlarkstruct.Struct)
	if !ok {
		return stacktrace.NewError("Cast to service didn't work")
	}

	task = &Task{ //nolint:exhaustruct
		Uuid:     strconv.Itoa(pyg.generateUuid()),
		TaskType: SHELL,
	}

	// get runcmd, image, env vars and set them in the yaml
	arguments := runShInstruction.GetInstruction().GetArguments()
	runCommand, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, tasks.RunArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.RunArgName)
	}
	task.RunCmd = []string{pyg.swapFutureReference(runCommand.GoString())}

	var image string
	if arguments.IsSet(tasks.ImageNameArgName) {
		imageStarlark, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, tasks.ImageNameArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.ImageNameArgName)
		}
		image = imageStarlark.GoString()
	} else {
		image = tasks.DefaultRunShImageName
	}
	task.Image = image

	var envVars []*EnvironmentVariable
	var envVarsMap map[string]string
	if arguments.IsSet(tasks.EnvVarsArgName) {
		envVarsStarlark, err := builtin_argument.ExtractArgumentValue[*starlark.Dict](arguments, tasks.EnvVarsArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.EnvVarsArgName)
		}
		if envVarsStarlark != nil && envVarsStarlark.Len() > 0 {
			var interpretationErr *startosis_errors.InterpretationError
			envVarsMap, interpretationErr = kurtosis_types.SafeCastToMapStringString(envVarsStarlark, tasks.EnvVarsArgName)
			if interpretationErr != nil {
				return interpretationErr
			}
		}
	}
	for key, val := range envVarsMap {
		envVars = append(envVars, &EnvironmentVariable{
			Key:   key,
			Value: val,
		})
	}
	task.EnvVars = envVars

	// for files:
	//	1. either the referenced files artifact already exists in the plan, in which case, look for it and reference it via instruction uuid
	// 	2. the referenced files artifact is new, in which case we add it to the plan
	if arguments.IsSet(tasks.FilesArgName) {
		filesStarlark, err := builtin_argument.ExtractArgumentValue[*starlark.Dict](arguments, tasks.FilesArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.FilesArgName)
		}
		if filesStarlark.Len() > 0 {
			filesArtifactMountDirPaths, interpretationErr := kurtosis_types.SafeCastToMapStringString(filesStarlark, tasks.FilesArgName)
			if interpretationErr != nil {
				return interpretationErr
			}
			for mountPath, fileArtifactName := range filesArtifactMountDirPaths {
				var filesArtifact *FilesArtifact
				// if there's already a files artifact that exists with this name from a previous instruction, reference that
				if potentialFilesArtifact, ok := pyg.filesArtifactIndex[fileArtifactName]; ok {
					filesArtifact = &FilesArtifact{ //nolint:exhaustruct
						Name: potentialFilesArtifact.Name,
						Uuid: potentialFilesArtifact.Uuid,
					}
				} else {
					// otherwise create a new one
					// the only information we have about a files artifact that didn't already exist is the name
					// if it didn't already exist AND interpretation was successful, it MUST HAVE been passed in via args
					filesArtifact = &FilesArtifact{ //nolint:exhaustruct
						Name: fileArtifactName,
						Uuid: strconv.Itoa(pyg.generateUuid()),
					}
					// add to the index and append to the plan yaml
					pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, filesArtifact)
					pyg.filesArtifactIndex[fileArtifactName] = filesArtifact
				}

				task.Files = append(task.Files, &FileMount{
					MountPath:      mountPath,
					FilesArtifacts: []*FilesArtifact{filesArtifact},
				})
			}
		}
	}

	// for store
	// - all files artifacts product from store are new files artifact that are added to the plan
	//		- add them to files artifacts list
	// 		- add them to the store section of run sh
	var store []*FilesArtifact
	storeSpecs, _ := tasks.ParseStoreFilesArg(pyg.serviceNetwork, arguments)
	// TODO: catch this error
	//if err != startosis_errors.WrapWithInterpretationError(nil, "") { catch this error
	//	return err
	//}
	for _, storeSpec := range storeSpecs {
		// add the FilesArtifact to list of all files artifacts and index
		uuid := strconv.Itoa(pyg.generateUuid())
		var newFilesArtifactFromStoreSpec = &FilesArtifact{
			Uuid:  uuid,
			Name:  storeSpec.GetName(),
			Files: []string{storeSpec.GetSrc()},
		}
		pyg.filesArtifactIndex[storeSpec.GetName()] = newFilesArtifactFromStoreSpec
		pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, newFilesArtifactFromStoreSpec)
		store = append(store, &FilesArtifact{ //nolint:exhaustruct
			Uuid: uuid,
			Name: storeSpec.GetName(),
		})
	}
	task.Store = store // TODO: be consistent about how I'm setting lists in the plan yamls, probably should add wrappers to the plan yaml

	// add task to index, do we even need a tasks index?
	pyg.planYaml.Tasks = append(pyg.planYaml.Tasks, task)
	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromRunPython(runPythonInstruction *instructions_plan.ScheduledInstruction) error {
	var task *Task

	// store future references

	task = &Task{ //nolint:exhaustruct
		Uuid:     strconv.Itoa(pyg.generateUuid()),
		TaskType: PYTHON,
	}

	// get run cmd, image and set them in the yaml
	arguments := runPythonInstruction.GetInstruction().GetArguments()
	runCommand, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, tasks.RunArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.RunArgName)
	}
	task.RunCmd = []string{runCommand.GoString()}

	var image string
	if arguments.IsSet(tasks.ImageNameArgName) {
		imageStarlark, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, tasks.ImageNameArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.ImageNameArgName)
		}
		image = imageStarlark.GoString()
	} else {
		image = tasks.DefaultRunShImageName
	}
	task.Image = image

	var envVars []*EnvironmentVariable
	var envVarsMap map[string]string
	if arguments.IsSet(tasks.EnvVarsArgName) {
		envVarsStarlark, err := builtin_argument.ExtractArgumentValue[*starlark.Dict](arguments, tasks.EnvVarsArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.EnvVarsArgName)
		}
		if envVarsStarlark != nil && envVarsStarlark.Len() > 0 {
			var interpretationErr *startosis_errors.InterpretationError
			envVarsMap, interpretationErr = kurtosis_types.SafeCastToMapStringString(envVarsStarlark, tasks.EnvVarsArgName)
			if interpretationErr != nil {
				return interpretationErr
			}
		}
	}
	for key, val := range envVarsMap {
		envVars = append(envVars, &EnvironmentVariable{
			Key:   key,
			Value: val,
		})
	}
	task.EnvVars = envVars

	// python args and python packages
	if arguments.IsSet(tasks.PythonArgumentsArgName) {
		argsValue, err := builtin_argument.ExtractArgumentValue[*starlark.List](arguments, tasks.PythonArgumentsArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "error occurred while extracting passed argument information")
		}
		argsList, sliceParsingErr := kurtosis_types.SafeCastToStringSlice(argsValue, tasks.PythonArgumentsArgName)
		if sliceParsingErr != nil {
			return startosis_errors.WrapWithInterpretationError(err, "error occurred while converting Starlark list of passed arguments to a golang string slice")
		}
		for idx, arg := range argsList {
			argsList[idx] = pyg.swapFutureReference(arg)
		}
		task.PythonArgs = append(task.PythonArgs, argsList...)
	}

	if arguments.IsSet(tasks.PackagesArgName) {
		packagesValue, err := builtin_argument.ExtractArgumentValue[*starlark.List](arguments, tasks.PackagesArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "error occurred while extracting packages information")
		}
		packagesList, sliceParsingErr := kurtosis_types.SafeCastToStringSlice(packagesValue, tasks.PackagesArgName)
		if sliceParsingErr != nil {
			return startosis_errors.WrapWithInterpretationError(err, "error occurred while converting Starlark list of packages to a golang string slice")
		}
		task.PythonPackages = append(task.PythonPackages, packagesList...)
	}

	// for files:
	//	1. either the referenced files artifact already exists in the plan, in which case, look for it and reference it via instruction uuid
	// 	2. the referenced files artifact is new, in which case we add it to the plan
	if arguments.IsSet(tasks.FilesArgName) {
		filesStarlark, err := builtin_argument.ExtractArgumentValue[*starlark.Dict](arguments, tasks.FilesArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", tasks.FilesArgName)
		}
		if filesStarlark.Len() > 0 {
			filesArtifactMountDirPaths, interpretationErr := kurtosis_types.SafeCastToMapStringString(filesStarlark, tasks.FilesArgName)
			if interpretationErr != nil {
				return interpretationErr
			}
			for mountPath, fileArtifactName := range filesArtifactMountDirPaths {
				var filesArtifact *FilesArtifact
				// if there's already a files artifact that exists with this name from a previous instruction, reference that
				if potentialFilesArtifact, ok := pyg.filesArtifactIndex[fileArtifactName]; ok {
					filesArtifact = &FilesArtifact{ //nolint:exhaustruct
						Uuid: potentialFilesArtifact.Uuid,
						Name: potentialFilesArtifact.Name,
					}
				} else {
					// otherwise create a new one
					// the only information we have about a files artifact that didn't already exist is the name
					// if it didn't already exist AND interpretation was successful, it MUST HAVE been passed in via args
					filesArtifact = &FilesArtifact{ //nolint:exhaustruct
						Name: fileArtifactName,
						Uuid: strconv.Itoa(pyg.generateUuid()),
					}
					// add to the index and append to the plan yaml
					pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, filesArtifact)
					pyg.filesArtifactIndex[fileArtifactName] = filesArtifact
				}

				task.Files = append(task.Files, &FileMount{
					MountPath:      mountPath,
					FilesArtifacts: []*FilesArtifact{filesArtifact},
				})
			}
		}
	}

	// for store
	// - all files artifacts product from store are new files artifact that are added to the plan
	//		- add them to files artifacts list
	// 		- add them to the store section of run sh
	var store []*FilesArtifact
	storeSpecs, _ := tasks.ParseStoreFilesArg(pyg.serviceNetwork, arguments)
	// TODO: catch this error
	//if err != startosis_errors.WrapWithInterpretationError(nil, "") { catch this error
	//	return err
	//}
	for _, storeSpec := range storeSpecs {
		// add the FilesArtifact to list of all files artifacts and index
		uuid := strconv.Itoa(pyg.generateUuid())
		var newFilesArtifactFromStoreSpec = &FilesArtifact{
			Uuid:  uuid,
			Name:  storeSpec.GetName(),
			Files: []string{storeSpec.GetSrc()},
		}
		pyg.filesArtifactIndex[storeSpec.GetName()] = newFilesArtifactFromStoreSpec
		pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, newFilesArtifactFromStoreSpec)
		store = append(store, &FilesArtifact{ //nolint:exhaustruct
			Uuid: uuid,
			Name: storeSpec.GetName(),
		})
	}
	task.Store = store // TODO: be consistent about how I'm setting lists in the plan yamls, probably should add wrappers to the plan yaml

	// add task to index, do we even need a tasks index?
	pyg.planYaml.Tasks = append(pyg.planYaml.Tasks, task)
	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromStoreServiceFiles(storeServiceFilesInstruction *instructions_plan.ScheduledInstruction) error {
	var filesArtifact *FilesArtifact

	// get the name of returned files artifact
	filesArtifactName, castErr := kurtosis_types.SafeCastToString(storeServiceFilesInstruction.GetReturnedValue(), "files artifact name")
	if castErr != nil {
		return castErr
	}
	filesArtifact = &FilesArtifact{ //nolint:exhaustruct
		Uuid: strconv.Itoa(pyg.generateUuid()),
		Name: filesArtifactName,
	}

	arguments := storeServiceFilesInstruction.GetInstruction().GetArguments()
	// set the uuid to be the uuid of the service that this files artifact comes from
	//serviceName, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, store_service_files.ServiceNameArgName)
	//if err != nil {
	//	return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", store_service_files.ServiceNameArgName)
	//}
	//if service, ok := pyg.serviceIndex[serviceName.GoString()]; !ok {
	//	return startosis_errors.NewInterpretationError("A service that hasn't been tracked was found on a store service instruction.")
	//} else {
	//	filesArtifact.Uuid = service.Uuid
	//}

	// parse for files
	src, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, store_service_files.SrcArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", store_service_files.SrcArgName)
	}
	filesArtifact.Files = []string{src.GoString()}

	// add it to the index and the plan yaml
	pyg.filesArtifactIndex[filesArtifactName] = filesArtifact
	pyg.planYaml.FilesArtifacts = append(pyg.planYaml.FilesArtifacts, filesArtifact)
	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromExec(execInstruction *instructions_plan.ScheduledInstruction) error {
	// TODO: update the plan yaml based on an add_service
	var task *Task

	arguments := execInstruction.GetInstruction().GetArguments()
	serviceNameArgumentValue, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, exec.ServiceNameArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", exec.ServiceNameArgName)
	}
	task = &Task{ //nolint:exhaustruct
		ServiceName: serviceNameArgumentValue.GoString(),
		TaskType:    EXEC,
		Uuid:        strconv.Itoa(pyg.generateUuid()),
	}

	execRecipe, err := builtin_argument.ExtractArgumentValue[*recipe.ExecRecipe](arguments, exec.RecipeArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", exec.RecipeArgName)
	}
	commandStarlarkList, _, interpretationErr := kurtosis_type_constructor.ExtractAttrValue[*starlark.List](execRecipe.KurtosisValueTypeDefault, recipe.CommandAttr)
	if interpretationErr != nil {
		return interpretationErr
	}
	// Convert Starlark list to Go slice
	cmdList, sliceParsingErr := kurtosis_types.SafeCastToStringSlice(commandStarlarkList, tasks.PythonArgumentsArgName)
	if sliceParsingErr != nil {
		return startosis_errors.WrapWithInterpretationError(err, "error occurred while converting Starlark list of passed arguments to a golang string slice")
	}
	for idx, cmd := range cmdList {
		cmdList[idx] = pyg.swapFutureReference(cmd)
	}
	task.RunCmd = cmdList

	acceptableCodes := []int64{0}
	if arguments.IsSet(exec.AcceptableCodesArgName) {
		acceptableCodesValue, err := builtin_argument.ExtractArgumentValue[*starlark.List](arguments, exec.AcceptableCodesArgName)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%v' argument", acceptableCodes)
		}
		acceptableCodes, err = kurtosis_types.SafeCastToIntegerSlice(acceptableCodesValue)
		if err != nil {
			return startosis_errors.WrapWithInterpretationError(err, "Unable to parse '%v' argument", acceptableCodes)
		}
	}
	task.AcceptableCodes = acceptableCodes

	pyg.planYaml.Tasks = append(pyg.planYaml.Tasks, task)
	return nil
}

func (pyg *PlanYamlGeneratorImpl) updatePlanYamlFromRemoveService(removeServiceInstruction *instructions_plan.ScheduledInstruction) error {
	arguments := removeServiceInstruction.GetInstruction().GetArguments()

	serviceName, err := builtin_argument.ExtractArgumentValue[starlark.String](arguments, remove_service.ServiceNameArgName)
	if err != nil {
		return startosis_errors.WrapWithInterpretationError(err, "Unable to extract value for '%s' argument", remove_service.ServiceNameArgName)
	}

	delete(pyg.serviceIndex, serviceName.GoString())

	for idx, service := range pyg.planYaml.Services {
		if service.Name == serviceName.GoString() {
			pyg.planYaml.Services[idx] = pyg.planYaml.Services[len(pyg.planYaml.Services)-1]
			pyg.planYaml.Services = pyg.planYaml.Services[:len(pyg.planYaml.Services)-1]
			return nil
		}
	}

	return nil
}

func convertPlanYamlToYaml(planYaml *PlanYaml) ([]byte, error) {
	// unravel all the indices and add them to the plan
	// add some sort of tie breaking so yaml's are deterministic

	yamlBytes, err := yaml.Marshal(planYaml)
	if err != nil {
		return []byte{}, err
	}
	return yamlBytes, nil
}

func getBuiltinNameFromInstruction(instruction *instructions_plan.ScheduledInstruction) string {
	return instruction.GetInstruction().GetCanonicalInstruction(false).GetInstructionName()
}

func (pyg *PlanYamlGeneratorImpl) generateUuid() int {
	pyg.uuidGenerator++
	return pyg.uuidGenerator
}

// if the string is a future reference, it swaps it out with what it should be
// else the string s is the same
func (pyg *PlanYamlGeneratorImpl) swapFutureReference(s string) string {
	newString := s
	for futureRef, swappedValue := range pyg.futureReferenceIndex {
		if strings.Contains(s, futureRef) {
			newString = strings.Replace(s, futureRef, swappedValue, -1)
		}
	}
	return newString
}
