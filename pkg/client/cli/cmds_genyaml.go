package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"github.com/telepresenceio/telepresence/v2/pkg/client"
	"github.com/telepresenceio/telepresence/v2/pkg/client/connector/userd_k8s"
	"github.com/telepresenceio/telepresence/v2/pkg/install"
	"github.com/telepresenceio/telepresence/v2/pkg/version"
)

type genYAMLInfo struct {
	outputFile string
	inputFile  string
}

func genYAMLCommand() *cobra.Command {
	info := genYAMLInfo{}
	cmd := &cobra.Command{
		Use:  "genyaml",
		Args: cobra.NoArgs,

		Short: "Generate YAML for use in kubernetes manifests.",
		Long: `Generate traffic-agent yaml for use in kubernetes manifests.
This allows the traffic agent to be injected by hand into existing kubernetes manifests.
For your modified workload to be valid, you'll have to manually inject both the container and the volume; you can do this by running "genyaml container" or "genyaml volume"
It is recommended that you not do this unless strictly necessary. Instead, we suggest use of the webhook injector to configure traffic agents.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("please run genyaml as \"genyaml container\" or \"genyaml volume\"")
		},
	}
	cmd.PersistentFlags().StringVar(&info.inputFile, "input", "",
		"Path to the yaml containing the workload definition (i.e. Deployment, StatefulSet, etc). Pass '-' for stdin.")
	cmd.PersistentFlags().StringVar(&info.outputFile, "output", "-",
		"Path to the file to place the output in. Defaults to '-' which means stdout.")
	_ = cmd.MarkPersistentFlagRequired("input")
	cmd.AddCommand(
		genContainerSubCommand(&info),
		genVolumeSubCommand(&info),
	)
	return cmd
}

func (i *genYAMLInfo) getOutputWriter() (io.WriteCloser, error) {
	if i.outputFile == "-" {
		return os.Stdout, nil
	}
	f, err := os.Create(i.outputFile)
	if err != nil {
		return nil, fmt.Errorf("unable to open output file %s: %w", i.outputFile, err)
	}
	return f, nil
}

func (i *genYAMLInfo) writeObjToOutput(obj interface{}) error {
	// So this sucks: Kubernetes structs don't have yaml serialization tags!
	// This means that we can't just yaml.Marshal the object. Now, we could use
	// the client-go to marshal it, but that's actually really hard given that
	// we're dealing with partial objects (e.g. containers, not pods).
	// However, it turns out that since k8s sends objects over the wire in json,
	// the structs do have json serialization tags; so if we serialize the object to json,
	// read it back as a plain old map, and then re-serialize to yaml, we'll get a reasonable result.
	doc, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("unable to marshal agent container: %w", err)
	}
	temp := map[string]interface{}{}
	err = json.Unmarshal(doc, &temp)
	if err != nil {
		// Be a bit weird if this happened, but okay.
		return fmt.Errorf("unable to unmarshal intermediate representation: %w", err)
	}
	doc, err = yaml.Marshal(&temp)
	if err != nil {
		return fmt.Errorf("unable to marshal intermediate representation to yaml: %w", err)
	}
	w, err := i.getOutputWriter()
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = w.Write(doc)
	if err != nil {
		return fmt.Errorf("unable to write to output %s: %w", i.outputFile, err)
	}
	return nil
}

func (i *genYAMLInfo) getInputReader() (io.ReadCloser, error) {
	if i.inputFile == "-" {
		return os.Stdin, nil
	}
	f, err := os.Open(i.inputFile)
	if err != nil {
		return nil, fmt.Errorf("unable to open input file %s: %w", i.inputFile, err)
	}
	return f, nil
}

type genContainerInfo struct {
	*genYAMLInfo
	containerName string
	serviceName   string
	appPort       int
	agentPort     int
	appProto      string
}

func genContainerSubCommand(yamlInfo *genYAMLInfo) *cobra.Command {
	info := genContainerInfo{genYAMLInfo: yamlInfo}
	cmd := &cobra.Command{
		Use:   "container",
		Args:  cobra.NoArgs,
		Short: "Generate YAML for the traffic-agent container.",
		Long:  "Generate YAML for the traffic-agent container. See genyaml for more info on what this means",
		RunE:  info.run,
	}
	cmd.Flags().StringVar(&info.containerName, "container-name", "",
		"The name of the container hosting the application you wish to intercept.")
	cmd.Flags().IntVar(&info.appPort, "port", 0,
		"The port number you wish to intercept")
	cmd.Flags().StringVar(&info.appProto, "protocol", string(corev1.ProtocolTCP),
		"The protocol the app's port speaks")
	cmd.Flags().IntVar(&info.agentPort, "agent-port", 9900,
		"The port number you wish the agent to listen on.")
	cmd.Flags().StringVar(&info.serviceName, "service-name", "",
		`The name of the service that's exposing this deployment and that you will wish to intercept.
Defaults to the name of the deployment.`)
	_ = cmd.MarkFlagRequired("container-name")
	_ = cmd.MarkFlagRequired("port")
	return cmd
}

func (i *genContainerInfo) run(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	f, err := i.getInputReader()
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("error reading from %s: %w", i.inputFile, err)
	}

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(schema.GroupVersion{Group: appsv1.GroupName, Version: "v1"}, &appsv1.StatefulSet{}, &appsv1.Deployment{}, &appsv1.ReplicaSet{})
	codecFactory := serializer.NewCodecFactory(scheme)
	deserializer := codecFactory.UniversalDeserializer()

	obj, kind, err := deserializer.Decode(b, nil, nil)
	if err != nil {
		return fmt.Errorf("unable to parse yaml in %s: %w", i.inputFile, err)
	}
	var containers []corev1.Container
	name := ""
	switch o := obj.(type) {
	case *appsv1.Deployment:
		containers = o.Spec.Template.Spec.Containers
		name = o.Name
	case *appsv1.StatefulSet:
		containers = o.Spec.Template.Spec.Containers
		name = o.Name
	case *appsv1.ReplicaSet:
		containers = o.Spec.Template.Spec.Containers
		name = o.Name
	default:
		return fmt.Errorf("unexpected object of kind %s; please pass in a Deployment or StatefulSet", kind)
	}
	containerIdx := -1
	for j, c := range containers {
		if c.Name == i.containerName {
			containerIdx = j
			break
		}
	}
	if containerIdx < 0 {
		return fmt.Errorf("container %s not found in %s given", i.containerName, kind)
	}
	container := &containers[containerIdx]

	if i.serviceName == "" {
		i.serviceName = name
	}

	cfg := client.GetConfig(ctx)
	k8sConfig, err := userd_k8s.NewConfig(ctx, kubeFlagMap())
	if err != nil {
		return fmt.Errorf("unable to get k8s config: %w", err)
	}

	registry := cfg.Images.Registry
	agentImage := cfg.Images.AgentImage
	// Use sane defaults if the user hasn't configured the registry and/or image
	if registry == "" {
		registry = "datawire"
	}
	if agentImage == "" {
		agentImage = "tel2:" + strings.TrimPrefix(version.Version, "v")
	}
	agentContainer := install.AgentContainer(
		i.serviceName,
		fmt.Sprintf("%s/%s", registry, agentImage),
		container,
		corev1.ContainerPort{
			Protocol:      corev1.Protocol(i.appProto),
			ContainerPort: int32(i.agentPort),
		},
		i.appPort,
		cfg.TelepresenceAPI.Port,
		k8sConfig.GetManagerNamespace(),
		false,
	)

	return i.writeObjToOutput(&agentContainer)
}

type genVolumeInfo struct {
	*genYAMLInfo
}

func genVolumeSubCommand(yamlInfo *genYAMLInfo) *cobra.Command {
	info := genVolumeInfo{genYAMLInfo: yamlInfo}
	cmd := &cobra.Command{
		Use:   "volume",
		Args:  cobra.NoArgs,
		Short: "Generate YAML for the traffic-agent volume.",
		Long:  "Generate YAML for the traffic-agent volume. See genyaml for more info on what this means",
		RunE:  info.run,
	}
	return cmd
}

func (i *genVolumeInfo) run(_ *cobra.Command, _ []string) error {
	volume := install.AgentVolume()
	return i.writeObjToOutput(&volume)
}
