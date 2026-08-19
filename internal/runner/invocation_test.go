package runner

import "testing"

func TestInvocationStringQuotesOnlyWhatNeedsIt(t *testing.T) {
	cases := []struct {
		name string
		inv  Invocation
		want string
	}{
		{
			name: "plain args stay bare",
			inv:  Invocation{Path: "/usr/bin/docker", Args: []string{"create", "--name", "plbx-demo"}},
			want: "/usr/bin/docker create --name plbx-demo",
		},
		{
			name: "paths and mounts stay bare",
			inv:  Invocation{Path: "docker", Args: []string{"--volume", "/home/viv/p:/home/viv/p:ro"}},
			want: "docker --volume /home/viv/p:/home/viv/p:ro",
		},
		{
			name: "spaces are quoted",
			inv:  Invocation{Path: "docker", Args: []string{"exec", "demo", "bash", "-lc", "echo hi"}},
			want: `docker exec demo bash -lc 'echo hi'`,
		},
		{
			name: "shell metacharacters are quoted",
			inv:  Invocation{Path: "docker", Args: []string{"-lc", "rm -rf / && echo $HOME"}},
			want: `docker -lc 'rm -rf / && echo $HOME'`,
		},
		{
			name: "embedded single quotes survive",
			inv:  Invocation{Path: "docker", Args: []string{"-lc", "echo 'hi'"}},
			want: `docker -lc 'echo '\''hi'\'''`,
		},
		{
			name: "an empty arg stays a distinct arg",
			inv:  Invocation{Path: "docker", Args: []string{"--env", ""}},
			want: "docker --env ''",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.inv.String(); got != tc.want {
				t.Errorf("String() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestContainerAndVolumeNamesAreNamespaced(t *testing.T) {
	if got := ContainerName("demo"); got != "plbx-demo" {
		t.Errorf("ContainerName = %q, want plbx-demo", got)
	}
	if got := homeVolume("demo"); got != "plbx-demo-home" {
		t.Errorf("homeVolume = %q, want plbx-demo-home", got)
	}
}
