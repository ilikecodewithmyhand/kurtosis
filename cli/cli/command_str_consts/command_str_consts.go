package command_str_consts

import (
	"os"
	"path"
)

// We put all the command strings here so that when we need to give users remediation instructions, we can give them the
//  commands they need to run
var KurtosisCmdStr = path.Base(os.Args[0])
const (
	CleanCmdStr = "clean"
	EnclaveCmdStr = "enclave"
		EnclaveInspectCmdStr = "inspect"
		EnclaveLsCmdStr = "ls"
		EnclaveNewCmdStr = "new"
		EnclaveStopCmdStr = "stop"
		EnclaveRmCmdStr = "rm"
	EngineCmdStr           = "engine"
		EngineStartCmdStr  = "start"
		EngineStatusCmdStr = "status"
		EngineStopCmdStr   = "stop"
	ModuleCmdStr = "module"
		ModuleExecCmdStr = "exec"
	ReplCmdStr = "repl"
		ReplInstallCmdStr = "install"
		ReplNewCmdStr = "new"
		ReplInspectCmdStr = "inspect"
	SandboxCmdStr = "sandbox"
	ServiceCmdStr = "service"
		ServiceLogsCmdStr = "logs"
	TestCmdStr = "test"
	VersionCmdStr = "version"
)

