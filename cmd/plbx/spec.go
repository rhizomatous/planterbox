package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
)

// specFlags are the create-time settings, shared by `create` and `run` because
// `run` creates a sandbox when none exists yet.
type specFlags struct {
	name   string
	image  string
	cpus   float64
	memory string
	ports  []string
	env    []string
	clone  bool
}

// bind registers the flags on cmd.
func (f *specFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.name, "name", "", "sandbox name (default: the workspace directory's name)")
	fl.StringVar(&f.image, "image", "", "base image to start from (default: the agent's own)")
	fl.Float64Var(&f.cpus, "cpus", 0, "limit on CPU, in cores (default: unlimited)")
	fl.StringVarP(&f.memory, "memory", "m", "", "memory limit, e.g. 8GiB (default: unlimited)")
	fl.StringArrayVarP(&f.ports, "publish", "p", nil, "publish a port, host:sandbox or a bare port (repeatable)")
	fl.StringArrayVarP(&f.env, "env", "e", nil, "environment variable, NAME=VALUE (repeatable)")
	fl.BoolVar(&f.clone, "clone", false,
		"work in a private clone; mount your repository read-only instead of read-write")
}

// parsePorts reads the --publish flags.
//
// Separate from buildSpec because ports are not part of a spec: they are not
// baked into the container, and can be changed on a sandbox that already
// exists. `create` and `run` publish them as a second step.
func (f *specFlags) parsePorts() ([]api.Port, error) {
	ports := make([]api.Port, 0, len(f.ports))
	for _, p := range f.ports {
		port, err := parsePort(p)
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	if err := api.ValidatePorts(ports); err != nil {
		return nil, err
	}
	return ports, nil
}

// changed reports whether any create-time flag was set. `run` uses this to warn
// when settings are given for a sandbox that already exists, since they are
// fixed at create time and would otherwise be silently ignored.
func (f *specFlags) changed(cmd *cobra.Command) []string {
	var set []string
	for _, name := range []string{"name", "image", "cpus", "memory", "env", "clone"} {
		if cmd.Flags().Changed(name) {
			set = append(set, "--"+name)
		}
	}
	return set
}

// buildSpec assembles a Spec from the flags and positional paths. cwd is the
// directory plbx was invoked from, and is the workspace when no path is given.
func (f *specFlags) buildSpec(agent string, paths []string, cwd string) (api.Spec, error) {
	def, err := api.LookupAgent(agent)
	if err != nil {
		return api.Spec{}, err
	}

	if len(paths) == 0 {
		paths = []string{cwd}
	}
	workspaces := make([]api.Workspace, 0, len(paths))
	for _, p := range paths {
		ws, err := parseWorkspace(p, cwd)
		if err != nil {
			return api.Spec{}, err
		}
		if err := mustBeDir(ws.Host); err != nil {
			return api.Spec{}, err
		}
		workspaces = append(workspaces, ws)
	}

	spec := api.Spec{
		Agent:      agent,
		Image:      def.Image,
		Workspaces: workspaces,
		Clone:      f.clone,
	}
	if f.image != "" {
		spec.Image = f.image
	}

	spec.Name = f.name
	if spec.Name == "" {
		spec.Name = api.SandboxName(workspaces[0].Host)
	}

	for _, e := range f.env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return api.Spec{}, fmt.Errorf("env %q: expected NAME=VALUE", e)
		}
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		spec.Env[k] = v
	}

	spec.Resources.CPUs = f.cpus
	if f.memory != "" {
		bytes, err := api.ParseBytes(f.memory)
		if err != nil {
			return api.Spec{}, err
		}
		spec.Resources.Memory = bytes
	}
	return spec, nil
}

// mustBeDir rejects a workspace that is not a directory, so the failure lands
// here rather than as an opaque mount error from the runtime.
func mustBeDir(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", path, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("workspace %s is not a directory", path)
	}
	return nil
}
