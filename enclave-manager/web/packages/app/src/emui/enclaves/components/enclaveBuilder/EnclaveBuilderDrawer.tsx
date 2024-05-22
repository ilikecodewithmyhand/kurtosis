import {
  Button,
  ButtonGroup,
  Drawer,
  DrawerBody,
  DrawerCloseButton,
  DrawerContent,
  DrawerFooter,
  DrawerHeader,
  DrawerOverlay,
  Flex,
  ListItem,
  Text,
  Tooltip,
  UnorderedList,
} from "@chakra-ui/react";
import { isDefined, KurtosisAlert, KurtosisAlertModal, RemoveFunctions, stringifyError } from "kurtosis-ui-components";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Edge, Node, ReactFlowProvider } from "reactflow";
import "reactflow/dist/style.css";
import { useEnclavesContext } from "../../EnclavesContext";
import { EnclaveFullInfo } from "../../types";
import { ViewStarlarkModal } from "./modals/ViewStarlarkModal";
import {PublishRepoModal} from "./modals/PublishRepoModal";
import { KurtosisNodeData } from "./types";
import { getInitialGraphStateFromEnclave } from "./utils";
import { useVariableContext, VariableContextProvider } from "./VariableContextProvider";
import { Visualiser, VisualiserImperativeAttributes } from "./Visualiser";

type EnclaveBuilderDrawerProps = {
  isOpen: boolean;
  onClose: () => void;
  existingEnclave?: RemoveFunctions<EnclaveFullInfo>;
};

const CLIENT_ID = ``;
const CLIENT_SECRET = ``;

async function exchangeCodeForToken(code: string): Promise<string> {
  const response = await fetch('https://github.com/login/oauth/access_token', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Accept': 'application/json'
    },
    body: JSON.stringify({
      client_id: CLIENT_ID,
      client_secret: CLIENT_SECRET,
      code: code
    })
  });
  const data = await response.json();
  return data.access_token;
}

export const EnclaveBuilderDrawer = (props: EnclaveBuilderDrawerProps) => {
  const variableContextKey = useRef(0);
  const [error, setError] = useState<string>();

  const {
    nodes: initialNodes,
    edges: initialEdges,
    data: initialData,
  } = useMemo((): {
    nodes: Node<any>[];
    edges: Edge<any>[];
    data: Record<string, KurtosisNodeData>;
  } => {
    variableContextKey.current += 1;
    const parseResult = getInitialGraphStateFromEnclave<KurtosisNodeData>(props.existingEnclave);
    if (parseResult.isErr) {
      setError(parseResult.error);
      return { nodes: [], edges: [], data: {} };
    }
    return {
      ...parseResult.value,
      data: Object.entries(parseResult.value.data)
        .filter(([id, data]) => parseResult.value.nodes.some((node) => node.id === id))
        .reduce((acc, [id, data]) => ({ ...acc, [id]: data }), {} as Record<string, KurtosisNodeData>),
    };
  }, [props.existingEnclave]);

  useEffect(() => {
    if (!props.isOpen) {
      variableContextKey.current += 1;
    }
  }, [props.isOpen]);

  if (isDefined(error)) {
    return (
      <KurtosisAlertModal
        title={"Error"}
        content={error}
        isOpen={true}
        onClose={() => {
          setError(undefined);
          props.onClose();
        }}
      />
    );
  }

  return (
    <VariableContextProvider key={variableContextKey.current} initialData={initialData}>
      <EnclaveBuilderDrawerImpl {...props} initialNodes={initialNodes} initialEdges={initialEdges} />
    </VariableContextProvider>
  );
};

type EnclaveBuilderDrawerImplProps = EnclaveBuilderDrawerProps & {
  initialNodes: Node[];
  initialEdges: Edge[];
};
const EnclaveBuilderDrawerImpl = ({
  isOpen,
  onClose,
  existingEnclave,
  initialNodes,
  initialEdges,
}: EnclaveBuilderDrawerImplProps) => {
  const navigator = useNavigate();
  const visualiserRef = useRef<VisualiserImperativeAttributes | null>(null);
  const { createEnclave, runStarlarkScript } = useEnclavesContext();
  const { data } = useVariableContext();
  const dataIssues = useMemo(
    () =>
      Object.values(data)
        .filter((nodeData) => !nodeData.isValid)
        .map((nodeData) => `${nodeData.type} ${nodeData.name} has invalid data`),
    [data],
  );
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [currentStarlarkPreview, setCurrentStarlarkPreview] = useState<string>();
  const [isPublishModalOpen, setIsPublishModalOpen] = useState(false);
  const [code, setCode] = useState<string>('');

  const handleRun = async () => {
    if (!isDefined(visualiserRef.current)) {
      setError("Cannot run when no services are defined");
      return;
    }

    setError(undefined);
    let enclave = existingEnclave;
    let enclaveUUID = existingEnclave?.shortenedUuid;
    if (!isDefined(existingEnclave)) {
      setIsLoading(true);
      const newEnclave = await createEnclave("", "info", true);
      setIsLoading(false);

      if (newEnclave.isErr) {
        setError(`Could not create enclave, got: ${newEnclave.error}`);
        return;
      }
      if (!isDefined(newEnclave.value.enclaveInfo)) {
        setError(`Did not receive enclave info when running createEnclave`);
        return;
      }
      enclave = newEnclave.value.enclaveInfo;
      enclaveUUID = newEnclave.value.enclaveInfo.shortenedUuid;
    }

    if (!isDefined(enclave)) {
      setError(`Cannot trigger starlark run as enclave info cannot be found`);
      return;
    }

    try {
      const logsIterator = await runStarlarkScript(enclave, visualiserRef.current.getStarlark(), {});
      onClose();
      navigator(`/enclave/${enclaveUUID}/logs`, { state: { logs: logsIterator } });
    } catch (error: any) {
      setError(stringifyError(error));
    }
  };

  const handlePreview = () => {
    setCurrentStarlarkPreview(visualiserRef.current?.getStarlark() || "Unable to render");
  };

    // TODO: implement publish to github
    // display a form fill to enter information about the repository
    // name of the package
    // kurtosis package icon
    //
    // need to authenticate to users github using web app OAuth flow - token and refresh token with proper perms
    // create a github app to do that
    // figure out what requests need to be made to GitHub api to create a repository and fill it with info
    // create a new repository on users behalf
    // commit code to that repository
    // readme.md
    //  add a button to share to twitter in the readme and in the step after publishing
    //  add a button in the readme that takes you straight to the Kurtosis package
    // main.star, kurtosis.yml, kurtosis-package-icon.png

    // const response = await fetch('https://github.com/login/oauth/access_token', {
    //   method: 'POST',
    //   headers: {
    //     'Content-Type': 'application/json',
    //     'Accept': 'application/json'
    //   },
    //   body: JSON.stringify({
    //     client_id: CLIENT_ID,
    //     client_secret: CLIENT_SECRET,
    //     code: code
    //   })
    // });
    // const data = await response.json();
    //
    // const accessToken = await exchangeCodeForToken(code, CLIENT_ID, CLIENT_SECRET);
    // const params = new URLSearchParams(window.location.search);
    // const code = params.get('code');
    // if (!code) return;
    //
    // console.log(accessToken);

  const handlePublish = () => {
    const REDIRECT_URI = `http://localhost:4000`
    const authUrl = `https://github.com/login/oauth/authorize?client_id=${CLIENT_ID}&scope=repo,read:user,workflow&redirect_uri=${encodeURIComponent(REDIRECT_URI)}`
    const windowFeatures = 'popup=yes, width=400, height=600';
    window.open(authUrl, undefined, windowFeatures);

    window.addEventListener('message', (event: MessageEvent) => {
      if (event.data.code){
        console.log(event.data.code)
        setCode(event.data.code);
        setIsPublishModalOpen(true)

      }
    }, false);
  };

  return (
    <Drawer size={"full"} isOpen={isOpen} onClose={!isLoading ? onClose : () => null} closeOnEsc={false}>
      <DrawerOverlay />
      <DrawerContent>
        <DrawerHeader>
          {isDefined(existingEnclave) ? `Editing ${existingEnclave.name}` : "Build a new Enclave"}
        </DrawerHeader>
        <DrawerCloseButton />
        <DrawerBody paddingInline={"0"} p={"0"}>
          {isDefined(error) && <KurtosisAlert message={error} />}
          <ReactFlowProvider>
            <Visualiser
              ref={visualiserRef}
              initialNodes={initialNodes}
              initialEdges={initialEdges}
              existingEnclave={existingEnclave}
            />
          </ReactFlowProvider>
        </DrawerBody>
        <DrawerFooter>
          <ButtonGroup>
            <Button onClick={onClose} isDisabled={isLoading}>
              Close
            </Button>
            <Button onClick={handlePreview}>Preview</Button>
            <Button onClick={handlePublish}>Publish</Button>
            <Tooltip
              label={
                dataIssues.length === 0 ? undefined : (
                  <Flex flexDirection={"column"}>
                    <Text>There are data issues that must be addressed before this enclave can run:</Text>
                    <UnorderedList>
                      {dataIssues.map((issue, i) => (
                        <ListItem key={i}>{issue}</ListItem>
                      ))}
                    </UnorderedList>
                  </Flex>
                )
              }
            >
              <Button
                onClick={handleRun}
                colorScheme={"green"}
                isLoading={isLoading}
                loadingText={"Run"}
                isDisabled={dataIssues.length > 0}
              >
                Run
              </Button>
            </Tooltip>
          </ButtonGroup>
        </DrawerFooter>
      </DrawerContent>
      <ViewStarlarkModal
        isOpen={isDefined(currentStarlarkPreview)}
        onClose={() => setCurrentStarlarkPreview(undefined)}
        starlark={currentStarlarkPreview}
      />
      <PublishRepoModal
        isOpen={isPublishModalOpen}
        onClose={() => setIsPublishModalOpen(false)}
        code={code}
        starlark={currentStarlarkPreview}
      />
    </Drawer>
  );
};
