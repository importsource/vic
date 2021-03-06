// Copyright 2016 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package upgrade

import (
	"path"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/urfave/cli"
	"github.com/vmware/vic/lib/install/data"
	"github.com/vmware/vic/lib/install/management"
	"github.com/vmware/vic/lib/install/validate"
	"github.com/vmware/vic/pkg/errors"
	"github.com/vmware/vic/pkg/trace"
	"github.com/vmware/vic/pkg/vsphere/vm"

	"golang.org/x/net/context"
)

// Upgrade has all input parameters for vic-machine upgrade command
type Upgrade struct {
	*data.Data

	executor *management.Dispatcher
}

func NewUpgrade() *Upgrade {
	upgrade := &Upgrade{}
	upgrade.Data = data.NewData()

	return upgrade
}

// Flags return all cli flags for upgrade
func (u *Upgrade) Flags() []cli.Flag {
	flags := []cli.Flag{
		cli.DurationFlag{
			Name:        "timeout",
			Value:       3 * time.Minute,
			Usage:       "Time to wait for upgrade",
			Destination: &u.Timeout,
		},
	}
	flags = append(
		append(
			append(
				append(
					append(
						u.TargetFlags(),
						u.IDFlags()...,
					), u.ComputeFlags()...,
				), u.ImageFlags()...,
			), flags...),
		u.DebugFlags()...,
	)
	return flags
}

func (u *Upgrade) processParams() error {
	defer trace.End(trace.Begin(""))

	if err := u.HasCredentials(); err != nil {
		return err
	}

	u.Insecure = true
	return nil
}

func (u *Upgrade) Run(cli *cli.Context) error {
	var err error
	if err = u.processParams(); err != nil {
		return err
	}

	if u.Debug.Debug > 0 {
		log.SetLevel(log.DebugLevel)
		trace.Logger.Level = log.DebugLevel
	}

	if len(cli.Args()) > 0 {
		log.Errorf("Unknown argument: %s", cli.Args()[0])
		return errors.New("invalid CLI arguments")
	}

	var images map[string]string
	if images, err = u.CheckImagesFiles(u.Force); err != nil {
		return err
	}

	log.Infof("### Upgrading VCH ####")

	ctx, cancel := context.WithTimeout(context.Background(), u.Timeout)
	defer cancel()

	validator, err := validate.NewValidator(ctx, u.Data)
	if err != nil {
		log.Errorf("Upgrade cannot continue - failed to create validator: %s", err)
		return errors.New("upgrade failed")
	}
	executor := management.NewDispatcher(validator.Context, validator.Session, nil, u.Force)

	var vch *vm.VirtualMachine
	if u.Data.ID != "" {
		vch, err = executor.NewVCHFromID(u.Data.ID)
	} else {
		vch, err = executor.NewVCHFromComputePath(u.Data.ComputeResourcePath, u.Data.DisplayName, validator)
	}
	if err != nil {
		log.Errorf("Failed to get Virtual Container Host %s", u.DisplayName)
		log.Error(err)
		return errors.New("upgrade failed")
	}

	log.Infof("")
	log.Infof("VCH ID: %s", vch.Reference().String())

	vchConfig, err := executor.GetVCHConfig(vch)
	if err != nil {
		log.Error("Failed to get Virtual Container Host configuration")
		log.Error(err)
		return errors.New("upgrade failed")
	}
	executor.InitDiagnosticLogs(vchConfig)

	// FIXME: add vchConfig validation here, to make the old vch config is compatible with new version

	vConfig := validator.AddDeprecatedFields(ctx, vchConfig, u.Data)
	vConfig.ImageFiles = images
	vConfig.ApplianceISO = path.Base(u.ApplianceISO)
	vConfig.BootstrapISO = path.Base(u.BootstrapISO)
	vConfig.RollbackTimeout = u.Timeout

	if err = executor.Upgrade(vch, vchConfig, vConfig); err != nil {
		// upgrade failed
		executor.CollectDiagnosticLogs()
		if err == nil {
			err = errors.New("upgrade failed")
		}
		return err
	}
	log.Infof("Completed successfully")

	return nil
}
