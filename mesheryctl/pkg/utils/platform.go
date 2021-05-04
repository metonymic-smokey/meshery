package utils

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/layer5io/meshery/mesheryctl/internal/cli/root/config"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"

	meshkitutils "github.com/layer5io/meshkit/utils"
	meshkitkube "github.com/layer5io/meshkit/utils/kubernetes"
)

var (
	// ManifestsFolder is where the Kubernetes manifests are stored
	ManifestsFolder = "manifests"
)

// GetManifestTreeURL returns the manifest tree url based on version
func GetManifestTreeURL(version string) (string, error) {
	url := "https://api.github.com/repos/layer5io/meshery/git/trees/" + version + "?recursive=1"
	resp, err := http.Get(url)
	if err != nil {
		return "", errors.Wrapf(err, "failed to make GET request to %s", url)
	}
	defer SafeClose(resp.Body)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read response body")
	}

	var manLis ManifestList

	err = json.Unmarshal([]byte(body), &manLis)
	if err != nil {
		return "", errors.Wrap(err, "failed to read response body")
	}
	for i := range manLis.Tree {
		if manLis.Tree[i].Path == "install/deployment_yamls/k8s" {
			return manLis.Tree[i].URL, nil
		}
	}
	return "", errors.New("could not find path: install/deployment_yamls/k8s in the manifest tree")
}

// ListManifests lists the manifest files stored in GitHub
func ListManifests(url string) ([]Manifest, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to make GET request to %s", url)
	}
	defer SafeClose(resp.Body)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}

	var manLis ManifestList

	err = json.Unmarshal([]byte(body), &manLis)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}

	return manLis.Tree, nil
}

// GetManifestURL returns the URLs for the manifest files
func GetManifestURL(manifest Manifest, rawManifestsURL string) string {
	var manifestURL string

	if manifest.Typ == "blob" {
		manifestURL = rawManifestsURL + manifest.Path
		return manifestURL
	}
	return ""
}

func GetOperatorURLs(operatorVersion string) map[string]string {
	urls := make(map[string]string)

	urls["MesheryOperator"] = baseConfigURL + operatorVersion + OperatorURL
	urls["MesheryBroker"] = baseConfigURL + operatorVersion + BrokerURL
	urls["MesheryMeshsync"] = baseConfigURL + operatorVersion + MeshsyncURL

	return urls
}

// DownloadManifests downloads all the Kubernetes manifest files
func DownloadManifests(manifestArr []Manifest, rawManifestsURL, operatorVersion string) error {
	for _, manifest := range manifestArr {
		if manifestFile := GetManifestURL(manifest, rawManifestsURL); manifestFile != "" {
			// download the manifest files to ~/.meshery/manifests folder
			filepath := filepath.Join(MesheryFolder, ManifestsFolder, manifest.Path)
			if err := DownloadFile(filepath, manifestFile); err != nil {
				return errors.Wrapf(err, SystemError(fmt.Sprintf("failed to download %s file from %s", filepath, manifestFile)))
			}
		}
	}

	operatorURLs := GetOperatorURLs(operatorVersion)

	operatorFilepath := filepath.Join(MesheryFolder, ManifestsFolder, MesheryOperator)
	err := DownloadFile(operatorFilepath, operatorURLs["MesheryOperator"])
	if err != nil {
		return errors.Wrapf(err, SystemError(fmt.Sprintf("failed to download %s file from %s operator file", operatorFilepath, MesheryOperator)))
	}

	brokerFilepath := filepath.Join(MesheryFolder, ManifestsFolder, MesheryOperatorBroker)
	err = DownloadFile(brokerFilepath, operatorURLs["MesheryBroker"])
	if err != nil {
		return errors.Wrapf(err, SystemError(fmt.Sprintf("failed to download %s file from %s operator file", brokerFilepath, MesheryOperatorBroker)))
	}

	meshsyncFilepath := filepath.Join(MesheryFolder, ManifestsFolder, MesheryOperatorMeshsync)
	err = DownloadFile(meshsyncFilepath, operatorURLs["MesheryMeshsync"])
	if err != nil {
		return errors.Wrapf(err, SystemError(fmt.Sprintf("failed to download %s file from %s operator file", meshsyncFilepath, MesheryOperatorMeshsync)))
	}

	return nil
}

// FetchManifests is a wrapper function that identifies the required manifest files as downloads them
func FetchManifests(version, operatorVersion string) ([]Manifest, error) {
	log.Debug("fetching required Kubernetes manifest files...")
	// get correct minfestsURL based on version
	manifestsURL, err := GetManifestTreeURL(version)
	if err != nil {
		return nil, errors.Wrap(err, "failed to make GET request")
	}
	// pick all the manifest files stored in minfestsURL
	manifests, err := ListManifests(manifestsURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to make GET request")
	}

	log.Debug("deleting ~/.meshery/manifests folder...")
	// delete manifests folder if it already exists
	if err := os.RemoveAll(ManifestsFolder); err != nil {
		return nil, err
	}
	log.Info("creating ~/.meshery/manifests folder...")
	// create a manifests folder under ~/.meshery to store the manifest files
	if err := os.MkdirAll(filepath.Join(MesheryFolder, ManifestsFolder), os.ModePerm); err != nil {
		return nil, errors.Wrapf(err, SystemError(fmt.Sprintf("failed to make %s directory", ManifestsFolder)))
	}
	log.Debug("created manifests folder...")

	gitHubFolder := "https://github.com/layer5io/meshery/tree/" + version + "/install/deployment_yamls/k8s"
	log.Info("downloading manifest files from ", gitHubFolder)

	// download all the manifest files to the ~/.meshery/manifests folder
	rawManifestsURL := "https://raw.githubusercontent.com/layer5io/meshery/" + version + "/install/deployment_yamls/k8s/"
	err = DownloadManifests(manifests, rawManifestsURL, operatorVersion)

	if err != nil {
		return nil, errors.Wrap(err, "failed to download manifests")
	}

	return manifests, nil
}

// GetLatestStableReleaseTag fetches and returns the latest release tag from GitHub
func GetLatestStableReleaseTag() (string, error) {
	url := "https://api.github.com/repos/layer5io/meshery/releases/latest"
	resp, err := http.Get(url)
	if err != nil {
		return "", errors.Wrapf(err, "failed to make GET request to %s", url)
	}
	defer SafeClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("failed to get latest stable release tag")
	}

	var dat map[string]interface{}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read response body")
	}
	if err := json.Unmarshal(body, &dat); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal json into object")
	}
	null := ""
	if dat["tag_name"] != nil {
		null = dat["tag_name"].(string)
	}
	return null, nil
}

// IsAdapterValid checks if the adapter mentioned by the user is a valid adapter
func IsAdapterValid(manifestArr []Manifest, adapterManifest string) bool {
	for _, v := range manifestArr {
		if v.Path == adapterManifest {
			return true
		}
	}

	return false
}

// DownloadDockerComposeFile fetches docker-compose.yaml based on passed context if it does not exists.
// Use force to override download anyway
func DownloadDockerComposeFile(ctx config.Context, force bool) error {
	if _, err := os.Stat(DockerComposeFile); os.IsNotExist(err) || force {
		fileURL := ""

		if ctx.Channel == "edge" {
			fileURL = "https://raw.githubusercontent.com/layer5io/meshery/master/docker-compose.yaml"
		} else if ctx.Channel == "stable" {
			if ctx.Version["meshery"] == "latest" {
				ctx.Version["meshery"], err = GetLatestStableReleaseTag()
				if err != nil {
					return errors.Wrapf(err, fmt.Sprintf("failed to fetch latest stable release tag"))
				}
			}
			fileURL = "https://raw.githubusercontent.com/layer5io/meshery/" + ctx.Version["meshery"] + "/docker-compose.yaml"
		} else {
			return errors.Errorf("unknown channel %s", ctx.Channel)
		}

		if err := DownloadFile(DockerComposeFile, fileURL); err != nil {
			return errors.Wrapf(err, SystemError(fmt.Sprintf("failed to download %s file from %s", DockerComposeFile, fileURL)))
		}
	}
	return nil
}

// ApplyManifest is a wrapper function for client.ApplyManifest
func ApplyManifest(manifest []byte, client *meshkitkube.Client, update bool, delete bool) error {
	// ApplyManifest applies/updates/deletes the given manifest file to/from the Kubernetes cluster
	err := client.ApplyManifest(manifest, meshkitkube.ApplyOptions{
		Namespace: MesheryNamespace,
		Update:    update,
		Delete:    delete,
	})

	if err != nil {
		return errors.Wrap(err, "failed to apply manifests")
	}
	return nil
}

// ApplyManifestFiles applies/updates/deletes all the required manifests into the Kubernetes cluster
func ApplyManifestFiles(manifestArr []Manifest, requestedAdapters []string, client *meshkitkube.Client, update bool, delete bool) error {
	// path to the manifest files ~/.meshery/manifests
	manifestFiles := filepath.Join(MesheryFolder, ManifestsFolder)

	// read the manifest files as strings
	// other than the adapters, meshery-deployment.yaml, meshery-service.yaml and service-account.yaml should be applied
	MesheryDeploymentManifest, err := meshkitutils.ReadLocalFile(filepath.Join(manifestFiles, MesheryDeployment))
	if err != nil {
		return errors.Wrap(err, "failed to read manifest files")
	}
	mesheryServiceManifest, err := meshkitutils.ReadLocalFile(filepath.Join(manifestFiles, MesheryService))
	if err != nil {
		return errors.Wrap(err, "failed to read manifest files")
	}
	serviceAccountManifest, err := meshkitutils.ReadLocalFile(filepath.Join(manifestFiles, ServiceAccount))
	if err != nil {
		return errors.Wrap(err, "failed to read manifest files")
	}

	// apply/update/delete the manifest files
	if err = ApplyManifest([]byte(MesheryDeploymentManifest), client, update, delete); err != nil {
		return err
	}
	if err = ApplyManifest([]byte(mesheryServiceManifest), client, update, delete); err != nil {
		return err
	}
	if err = ApplyManifest([]byte(serviceAccountManifest), client, update, delete); err != nil {
		return err
	}

	// loop through the required adapters as specified in the config.yaml file and apply/update/delete each
	for _, adapter := range requestedAdapters {
		// for each adapter, there is a meshery-adapterName-deployment.yaml and meshery-adapterName-service.yaml
		// manifest file. See- https://github.com/layer5io/meshery/tree/master/install/deployment_yamls/k8s
		adapterFile := filepath.Join(manifestFiles, adapter)
		adapterDeployment := adapterFile + "-deployment.yaml"
		adapterService := adapterFile + "-service.yaml"

		if !IsAdapterValid(manifestArr, adapter+"-deployment.yaml") {
			return fmt.Errorf("invalid adapter %s specified. Please check %s/config.yaml file", adapter, MesheryFolder)
		}

		// read manifest files as strings and apply/update/delete
		manifestDepl, err := meshkitutils.ReadLocalFile(adapterDeployment)
		if err != nil {
			return errors.Wrap(err, "failed to read manifest files")
		}
		manifestService, err := meshkitutils.ReadLocalFile(adapterService)
		if err != nil {
			return errors.Wrap(err, "failed to read manifest files")
		}

		if err = ApplyManifest([]byte(manifestDepl), client, update, delete); err != nil {
			return err
		}
		if err = ApplyManifest([]byte(manifestService), client, update, delete); err != nil {
			return err
		}
	}

	//applying meshery operator files
	MesheryOperatorManifest, err := meshkitutils.ReadLocalFile(filepath.Join(manifestFiles, MesheryOperator))
	if err != nil {
		return errors.Wrap(err, "failed to read operator manifest files")
	}

	if err = ApplyManifest([]byte(MesheryOperatorManifest), client, update, delete); err != nil {
		return err
	}

	MesheryBrokerManifest, err := meshkitutils.ReadLocalFile(filepath.Join(manifestFiles, MesheryOperatorBroker))
	if err != nil {
		return errors.Wrap(err, "failed to read operator manifest files")
	}

	if err = ApplyManifest([]byte(MesheryBrokerManifest), client, update, delete); err != nil {
		return err
	}

	MesheryMeshsyncManifest, err := meshkitutils.ReadLocalFile(filepath.Join(manifestFiles, MesheryOperatorMeshsync))
	if err != nil {
		return errors.Wrap(err, "failed to read operator manifest files")
	}

	if err = ApplyManifest([]byte(MesheryMeshsyncManifest), client, update, delete); err != nil {
		return err
	}

	log.Debug("applied manifests to the Kubernetes cluster.")

	return nil
}

func ChangeManifestVersion(fileName string, version string, filePath string) error {
	// setting up config type to yaml files
	ViperCompose.SetConfigType("yaml")

	// setting up config file
	ViperCompose.SetConfigFile(filePath)
	err := ViperCompose.ReadInConfig()
	if err != nil {
		return fmt.Errorf("unable to read config %s | %s", fileName, err)
	}

	compose := K8sCompose{}
	yamlFile, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}

	// unmarshal the file into structs
	err = yaml.Unmarshal(yamlFile, &compose)
	if err != nil {
		return fmt.Errorf("unable to unmarshal config %s | %s", fileName, err)
	}
	image := compose.Spec.Template.Spec.Containers[0].Image
	spliter := strings.Split(image, ":")
	compose.Spec.Template.Spec.Containers[0].Image = fmt.Sprintf("%s:%s-%s", spliter[0], "stable", version)

	log.Debug(image, " changed to ", compose.Spec.Template.Spec.Containers[0].Image)

	ViperCompose.Set("apiVersion", compose.APIVersion)
	ViperCompose.Set("kind", compose.Kind)
	ViperCompose.Set("metadata", compose.Metadata)
	ViperCompose.Set("spec", compose.Spec)
	ViperCompose.Set("status", compose.Status)

	// Marshal the structs
	newConfig, err := yaml.Marshal(compose)
	if err != nil {
		return fmt.Errorf("unable to marshal config %s | %s", fileName, err)
	}
	err = ioutil.WriteFile(filePath, newConfig, 0644)
	if err != nil {
		return fmt.Errorf("unable to update config %s | %s", fileName, err)
	}

	return nil
}
