/*
Copyright 2019 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
	kcmd "k8s.io/kubectl/pkg/cmd"
	"k8s.io/minikube/pkg/minikube/config"
	"k8s.io/minikube/pkg/minikube/constants"
	"k8s.io/minikube/pkg/minikube/detect"
	"k8s.io/minikube/pkg/minikube/machine"
	"k8s.io/minikube/pkg/minikube/mustload"
	"k8s.io/minikube/pkg/minikube/node"
	"k8s.io/minikube/pkg/minikube/out"
	"k8s.io/minikube/pkg/minikube/vmpath"
)

var (
	useSSH bool
)

// kubectlCmd represents the kubectl command
var kubectlCmd = &cobra.Command{
	Use:   "kubectl",
	Short: "Run a kubectl binary matching the cluster version",
	Long: `Run the Kubernetes client, download it if necessary. Remember -- after kubectl!

This will run the Kubernetes client (kubectl) with the same version as the cluster

Normally it will download a binary matching the host operating system and architecture,
but optionally you can also run it directly on the control plane over the ssh connection.
This can be useful if you cannot run kubectl locally for some reason, like unsupported
host. Please be aware that when using --ssh all paths will apply to the remote machine.`,
	Example: "minikube kubectl -- --help\nminikube kubectl -- get pods --namespace kube-system",
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Run kubectl __complete $@ to get completions
		var out bytes.Buffer
		completeCmd := kcmd.NewDefaultKubectlCommand()
		completeCmd.SetArgs(append([]string{cobra.ShellCompRequestCmd}, append(args, toComplete)...))
		completeCmd.SetOut(&out)
		if err := completeCmd.Execute(); err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		// Scan completions
		var completions []string
		scanner := bufio.NewScanner(&out)
		for scanner.Scan() {
			// Strip completion description after tab
			completions = append(completions, strings.Split(scanner.Text(), "\t")[0])
		}
		if scanner.Err() != nil || len(completions) == 0 {
			return nil, cobra.ShellCompDirectiveError
		}

		// The final line of completions will be a directive (colon + int)
		directive, err := strconv.Atoi(strings.TrimPrefix(completions[len(completions)-1], ":"))
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		return completions[:len(completions)-1], cobra.ShellCompDirective(directive)
	},
	Run: func(cmd *cobra.Command, args []string) {
		cc, err := config.Load(ClusterFlagValue())

		version := constants.DefaultKubernetesVersion
		binaryMirror := ""
		if err == nil {
			version = cc.KubernetesConfig.KubernetesVersion
			binaryMirror = cc.BinaryMirror
		}

		cname := ClusterFlagValue()

		if useSSH {
			co := mustload.Running(cname)
			n := co.CP.Node

			kc := []string{"sudo"}
			kc = append(kc, kubectlPath(*co.Config))
			kc = append(kc, "--kubeconfig")
			kc = append(kc, kubeconfigPath())
			args = append(kc, args...)

			klog.Infof("Running SSH %v", args)
			err := machine.CreateSSHShell(co.API, *co.Config, *n, args, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error running kubectl: %v", err)
				os.Exit(1)
			}
			return
		}

		supported := false
		arch := detect.RuntimeArch()
		for _, a := range constants.SupportedArchitectures {
			if arch == a {
				supported = true
				break
			}
		}
		if !supported {
			fmt.Fprintf(os.Stderr, "Not supported on: %s\n", arch)
			os.Exit(1)
		}

		if len(args) > 1 && args[0] != "--help" && args[0] != cobra.ShellCompRequestCmd {
			cluster := []string{"--cluster", cname}
			args = append(cluster, args...)
		}

		c, err := KubectlCommand(version, binaryMirror, args...)
		if err != nil {
			out.ErrLn("Error caching kubectl: %v", err)
			os.Exit(1)
		}

		klog.Infof("Running %s %v", c.Path, args)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			var rc int
			if exitError, ok := err.(*exec.ExitError); ok {
				waitStatus := exitError.Sys().(syscall.WaitStatus)
				rc = waitStatus.ExitStatus()
			} else {
				fmt.Fprintf(os.Stderr, "Error running %s: %v\n", c.Path, err)
				rc = 1
			}
			os.Exit(rc)
		}
	},
}

// kubectlPath returns the path to kubectl
func kubectlPath(cfg config.ClusterConfig) string {
	return path.Join(vmpath.GuestPersistentDir, "binaries", cfg.KubernetesConfig.KubernetesVersion, "kubectl")
}

// kubeconfigPath returns the path to kubeconfig
func kubeconfigPath() string {
	return "/etc/kubernetes/admin.conf"
}

// KubectlCommand will return kubectl command with a version matching the cluster
func KubectlCommand(version, binaryURL string, args ...string) (*exec.Cmd, error) {
	if version == "" {
		version = constants.DefaultKubernetesVersion
	}

	path, err := node.CacheKubectlBinary(version, binaryURL)
	if err != nil {
		return nil, err
	}

	return exec.Command(path, args...), nil
}

func init() {
	kubectlCmd.Flags().BoolVar(&useSSH, "ssh", false, "Use SSH for running kubernetes client on the node")
}
