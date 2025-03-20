/**
# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package ldcache

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/urfave/cli/v2"

	"github.com/NVIDIA/nvidia-container-toolkit/internal/config"
	"github.com/NVIDIA/nvidia-container-toolkit/internal/logger"
	"github.com/NVIDIA/nvidia-container-toolkit/internal/oci"
	safeexec "github.com/NVIDIA/nvidia-container-toolkit/internal/safe-exec"
)

const (
	// ldsoconfdFilenamePattern specifies the pattern for the filename
	// in ld.so.conf.d that includes references to the specified directories.
	// The 00-nvcr prefix is chosen to ensure that these libraries have a
	// higher precedence than other libraries on the system, but lower than
	// the 00-cuda-compat that is included in some containers.
	ldsoconfdFilenamePattern = "00-nvcr-*.conf"
)

type command struct {
	safeexec.Execer
	logger logger.Interface
}

type options struct {
	folders       cli.StringSlice
	ldconfigPath  string
	containerSpec string
}

// NewCommand constructs an update-ldcache command with the specified logger
func NewCommand(logger logger.Interface) *cli.Command {
	c := command{
		logger: logger,
		Execer: safeexec.New(logger),
	}
	return c.build()
}

// build the update-ldcache command
func (m command) build() *cli.Command {
	cfg := options{}

	// Create the 'update-ldcache' command
	c := cli.Command{
		Name:  "update-ldcache",
		Usage: "Update ldcache in a container by running ldconfig",
		Before: func(c *cli.Context) error {
			return m.validateFlags(c, &cfg)
		},
		Action: func(c *cli.Context) error {
			return m.run(c, &cfg)
		},
	}

	c.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:        "folder",
			Usage:       "Specify a folder to add to /etc/ld.so.conf before updating the ld cache",
			Destination: &cfg.folders,
		},
		&cli.StringFlag{
			Name:        "ldconfig-path",
			Usage:       "Specify the path to the ldconfig program",
			Destination: &cfg.ldconfigPath,
			Value:       "/sbin/ldconfig",
		},
		&cli.StringFlag{
			Name:        "container-spec",
			Usage:       "Specify the path to the OCI container spec. If empty or '-' the spec will be read from STDIN",
			Destination: &cfg.containerSpec,
		},
	}

	return &c
}

func (m command) validateFlags(c *cli.Context, cfg *options) error {
	if cfg.ldconfigPath == "" {
		return errors.New("ldconfig-path must be specified")
	}
	return nil
}

func (m command) run(c *cli.Context, cfg *options) error {
	s, err := oci.LoadContainerState(cfg.containerSpec)
	if err != nil {
		return fmt.Errorf("failed to load container state: %v", err)
	}

	containerRootDirPath, err := s.GetContainerRootDirPath()
	if err != nil || containerRootDirPath == "" || containerRootDirPath == "/" {
		return fmt.Errorf("failed to determined container root: %v", err)
	}

	ldconfigPath := config.ResolveLDConfigPathOnHost(cfg.ldconfigPath)
	args := []string{
		filepath.Base(ldconfigPath),
		// Run ldconfig in the container root directory on the host.
		"-r", string(containerRootDirPath),
		// Explicitly specify using /etc/ld.so.conf since the host's ldconfig may
		// be configured to use a different config file by default.
		// Note that since we apply the `-r {{ .containerRootDir }}` argument, /etc/ld.so.conf is
		// in the container.
		"-f", "/etc/ld.so.conf",
	}

	if containerRootDirPath.HasPath("/etc/ld.so.cache") {
		args = append(args, "-C", "/etc/ld.so.cache")
	} else {
		m.logger.Debugf("No ld.so.cache found, skipping update")
		args = append(args, "-N")
	}

	folders := cfg.folders.Value()
	if containerRootDirPath.HasPath("/etc/ld.so.conf.d") {
		err := containerRootDirPath.CreateLdsoconfdFile(ldsoconfdFilenamePattern, folders...)
		if err != nil {
			return fmt.Errorf("failed to update ld.so.conf.d: %v", err)
		}
	} else {
		args = append(args, folders...)
	}

	return m.Exec(ldconfigPath, args, nil)
}
