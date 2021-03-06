// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2018 Datadog, Inc.

package flare

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	log "github.com/cihub/seelog"
	"github.com/mholt/archiver"

	"github.com/DataDog/datadog-agent/pkg/config"
	"github.com/DataDog/datadog-agent/pkg/status"
	"github.com/DataDog/datadog-agent/pkg/util"
	"github.com/DataDog/datadog-agent/pkg/util/kubernetes/apiserver"
)

// CreateDCAArchive packages up the files
func CreateDCAArchive(local bool, distPath, logFilePath string) (string, error) {
	zipFilePath := getArchivePath()
	confSearchPaths := SearchPaths{
		"":     config.Datadog.GetString("confd_dca_path"),
		"dist": filepath.Join(distPath, "conf.d"),
	}
	return createDCAArchive(zipFilePath, local, confSearchPaths, logFilePath)
}

func createDCAArchive(zipFilePath string, local bool, confSearchPaths SearchPaths, logFilePath string) (string, error) {
	b := make([]byte, 10)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}

	dirName := hex.EncodeToString([]byte(b))
	tempDir, err := ioutil.TempDir("", dirName)
	if err != nil {
		return "", err
	}

	defer os.RemoveAll(tempDir)

	// Get hostname, if there's an error in getting the hostname,
	// set the hostname to unknown
	hostname, err := util.GetHostname()
	if err != nil {
		hostname = "unknown"
	}

	// If the request against the API does not go through we don't collect the status log.
	if local {
		f := filepath.Join(tempDir, hostname, "local")
		err = ensureParentDirsExist(f)
		if err != nil {
			return "", err
		}

		err = ioutil.WriteFile(f, []byte{}, os.ModePerm)
		if err != nil {
			return "", err
		}
	} else {
		// The Status will be unavailable unless the agent is running.
		// Only zip it up if the agent is running
		err = zipDCAStatusFile(tempDir, hostname)
		if err != nil {
			log.Infof("Error getting the status of the DCA, %q", err)
			return "", err
		}
	}

	err = zipLogFiles(tempDir, hostname, logFilePath)
	if err != nil {
		return "", err
	}

	err = zipConfigFiles(tempDir, hostname, confSearchPaths)

	if err != nil {
		return "", err
	}

	err = zipExpVar(tempDir, hostname)
	if err != nil {
		return "", err
	}

	err = zipEnvvars(tempDir, hostname)
	if err != nil {
		return "", err
	}

	err = zipMetadataMap(tempDir, hostname)
	if err != nil {
		return "", err
	}

	err = archiver.Zip.Make(zipFilePath, []string{filepath.Join(tempDir, hostname)})
	if err != nil {
		return "", err
	}

	return zipFilePath, nil
}

func zipDCAStatusFile(tempDir, hostname string) error {
	// Grab the status
	log.Infof("Zipping the status at %s for %s", tempDir, hostname)
	s, err := status.GetAndFormatDCAStatus()
	if err != nil {
		log.Infof("Error zipping the status: %q", err)
		return err
	}

	// Clean it up
	cleaned, err := credentialsCleanerBytes(s)
	if err != nil {
		log.Infof("Error redacting the log files: %q", err)
		return err
	}

	f := filepath.Join(tempDir, hostname, "cluster-agent-status.log")
	log.Infof("Flare status made at %s", tempDir)
	err = ensureParentDirsExist(f)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(f, cleaned, os.ModePerm)
	if err != nil {
		return err
	}
	return err
}

func zipMetadataMap(tempDir, hostname string) error {
	// Grab the metadata map for all nodes.
	metaList, err := apiserver.GetMetadataMapBundleOnAllNodes()
	if err != nil {
		log.Infof("Error while collecting the cluster level metadata: %q", err)
	}

	metaBytes, err := json.Marshal(metaList)
	if err != nil {
		log.Infof("Error while marshalling the cluster level metadata: %q", err)
		return err
	}
	// Clean it up
	cleanedMetaBytes, err := credentialsCleanerBytes(metaBytes)
	if err != nil {
		log.Infof("Error redacting the log files: %q", err)
		return err
	}

	str, err := status.FormatMetadataMapCLI(cleanedMetaBytes)
	if err != nil {
		log.Infof("Error while rendering the cluster level metadata: %q", err)
		return err
	}

	sByte := []byte(str)
	f := filepath.Join(tempDir, hostname, "cluster-agent-metadatamapper.log")
	log.Infof("Flare metadata mapper made at %s", tempDir)
	err = ensureParentDirsExist(f)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(f, sByte, os.ModePerm)
	if err != nil {
		return err
	}
	return err
}
