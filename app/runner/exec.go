package runner

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/hashicorp/go-hclog"
)

const (
	consulFlavorOSS        = "oss"
	consulFlavorEnterprise = "ent"
)

type Runner struct {
	logger hclog.Logger

	devconsulBin string // special

	consulBin   string
	tfBin       string
	dockerBin   string
	minikubeBin string // optional
	kubectlBin  string // optional

	consulBinFlavor string // oss/ent
}

func Load(logger hclog.Logger, kubernetesEnabled bool) (*Runner, error) {
	r := &Runner{
		logger: logger,
	}

	var err error

	r.devconsulBin, err = os.Executable()
	if err != nil {
		return nil, err
	}

	type item struct {
		name string
		dest *string
		warn string // optional
	}
	lookup := []item{
		{"consul", &r.consulBin, "run 'make dev' from your consul checkout"},
		{"docker", &r.dockerBin, ""},
		{"terraform", &r.tfBin, ""},
	}
	if kubernetesEnabled {
		lookup = append(lookup,
			item{"minikube", &r.minikubeBin, ""},
			item{"kubectl", &r.kubectlBin, ""},
		)
	}

	var bins []string
	for _, i := range lookup {
		*i.dest, err = exec.LookPath(i.name)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				if i.warn != "" {
					return nil, fmt.Errorf("Could not find %q on path (%s): %w", i.name, i.warn, err)
				} else {
					return nil, fmt.Errorf("Could not find %q on path: %w", i.name, err)
				}
			}
			return nil, fmt.Errorf("Unexpected failure looking for %q on path: %w", i.name, err)
		}
		bins = append(bins, *i.dest)
	}
	r.logger.Trace("using binaries", "paths", bins)

	isEnterprise, err := checkIfConsulIsEnterprise(r.consulBin)
	if err != nil {
		return nil, err
	}

	if isEnterprise {
		r.consulBinFlavor = consulFlavorEnterprise
	} else {
		r.consulBinFlavor = consulFlavorOSS
	}

	return r, nil
}

func checkIfConsulIsEnterprise(consulBin string) (bool, error) {
	var w bytes.Buffer

	cmd := exec.Command(consulBin, "version")
	cmd.Stdout = &w
	cmd.Stderr = &w
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("could not invoke 'consul' to check its version: %w", err)
	}

	scan := bufio.NewScanner(&w)
	if !scan.Scan() {
		return false, errors.New("'consul version' output is unexpected")
	}
	line := scan.Text()

	return strings.HasSuffix(line, "+ent"), nil
}

func (r *Runner) DockerExec(args []string, stdout io.Writer) error {
	return cmdExec("docker", r.dockerBin, args, stdout, "")
}

func (r *Runner) MinikubeExec(args []string, stdout io.Writer) error {
	return cmdExec("minikube", r.minikubeBin, args, stdout, "")
}

func (r *Runner) KubectlExec(args []string, stdout io.Writer) error {
	return cmdExec("kubectl", r.kubectlBin, args, stdout, "")
}

func (r *Runner) TerraformExec(args []string, stdout io.Writer) error {
	return cmdExec("terraform", r.tfBin, args, stdout, "")
}

func (r *Runner) ConsulExec(args []string, stdout io.Writer, dir string) error {
	return cmdExec("consul", r.consulBin, args, stdout, dir)
}

func cmdExec(name, binary string, args []string, stdout io.Writer, dir string) error {
	if binary == "" {
		panic("binary named " + name + " was not detected")
	}
	var errWriter bytes.Buffer

	if stdout == nil {
		stdout = os.Stdout // TODO: wrap logs
	}

	cmd := exec.Command(binary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = stdout
	cmd.Stderr = &errWriter
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return &ExecError{
			BinaryName:  name,
			Err:         err,
			ErrorOutput: errWriter.String(),
		}
	}

	return nil
}

type ExecError struct {
	BinaryName  string
	ErrorOutput string
	Err         error
}

func (e *ExecError) Unwrap() error {
	return e.Err
}

func (e *ExecError) Error() string {
	return fmt.Sprintf(
		"could not invoke %q: %v : %s",
		e.BinaryName,
		e.Err,
		e.ErrorOutput,
	)
}

func (r *Runner) GetPathToSelf() string {
	if r.consulBinFlavor == "" {
		panic("our own binary was not analyzed")
	}
	return r.devconsulBin
}

func (r *Runner) IsOSS() bool {
	if r.consulBinFlavor == "" {
		panic("default consul binary was not analyzed")
	}
	return r.consulBinFlavor == consulFlavorOSS
}

func (r *Runner) IsEnterprise() bool {
	if r.consulBinFlavor == "" {
		panic("default consul binary was not analyzed")
	}
	return r.consulBinFlavor == consulFlavorEnterprise
}
