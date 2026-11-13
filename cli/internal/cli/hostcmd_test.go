package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/workspace"
)

// The keys a NixOS host takes from ~/.ssh/authorized_keys: its key lines,
// options and comments kept; no comment lines, no blank ones.
func TestAuthorizedKeys(t *testing.T) {
	home := t.TempDir()
	if got := authorizedKeys(home); got != nil {
		t.Errorf("no file: %v, want none", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# keys for jane\n\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOnlyForTests jane@example.com\r\n" +
		"   \n  from=\"192.0.2.0/24\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleOtherKeyForTests  \n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOnlyForTests jane@example.com",
		`from="192.0.2.0/24" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleOtherKeyForTests`,
	}
	if got := authorizedKeys(home); !reflect.DeepEqual(got, want) {
		t.Errorf("authorizedKeys = %q\nwant %q", got, want)
	}
}

// A NixOS host's files replace the template's machine/ with the carried
// /etc/nixos, every carried file under it, the host's own file kept.
func TestCarryInto(t *testing.T) {
	files := map[string][]byte{
		filepath.Join("hosts", "box", "default.nix"):                  []byte("host"),
		filepath.Join("hosts", "box", "machine", "configuration.nix"): []byte("template"),
	}
	carryInto(files, filepath.Join("hosts", "box"), map[string][]byte{
		"configuration.nix": []byte("carried"),
		"sub/dir.nix":       []byte("sub"),
	})
	want := map[string][]byte{
		filepath.Join("hosts", "box", "default.nix"):                  []byte("host"),
		filepath.Join("hosts", "box", "machine", "configuration.nix"): []byte("carried"),
		filepath.Join("hosts", "box", "machine", "sub", "dir.nix"):    []byte("sub"),
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("files %q\nwant %q", files, want)
	}
}

// Taking a NixOS host's files back leaves no hosts/<name>/ behind (a re-run
// would find it there and refuse), and hosts/ itself stays.
func TestRemoveHostFilesLeavesNoDirectory(t *testing.T) {
	dir := t.TempDir()
	rels := []string{"hosts/box/default.nix", "hosts/box/machine/configuration.nix", "hosts/box/machine/sub/dir.nix"}
	for _, rel := range append(rels, "hosts/other.nix") {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	(&App{Stderr: os.Stderr, Stdout: os.Stdout}).removeHostFiles(dir, rels)
	if _, err := os.Lstat(filepath.Join(dir, "hosts", "box")); !os.IsNotExist(err) {
		t.Errorf("hosts/box: %v, want it gone", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "hosts", "other.nix")); err != nil {
		t.Errorf("hosts/other.nix: %v, want it kept", err)
	}
}

// After host add or init on NixOS, a host whose networking.hostName isn't
// its name gets a warning and the setting that keeps the name: on a
// Proxmox container, its module's manageHostName; on a Proxmox VM with
// cloud-init, the VM's name in Proxmox or cloud-init off; else a
// mkOverride 49, which beats a module's mkForce. Never a mkForce: beside
// the Proxmox image's own mkForce "" it fails the build
// (TestHostNameAdviceNix).
func TestHostNameWarning(t *testing.T) {
	const host = "hosts/box/default.nix"
	empty := `The switch won't name this machine box: its networking.hostName is "" (a module the host imports leaves the name to Proxmox, cloud-init or DHCP), and piste finds this machine's host by its hostname.`
	for _, c := range []struct {
		name string
		f    hostNameFacts
		want []string
	}{
		{"its name", hostNameFacts{HostName: str("box")}, nil},
		{"its name, cloud-init on", hostNameFacts{HostName: str("box"), ProxmoxCloudInit: true}, nil},
		{"a Proxmox container", hostNameFacts{HostName: str(""), ProxmoxLXC: true}, []string{
			empty,
			`  To keep box: set proxmoxLXC.manageHostName = true; in hosts/box/default.nix, or name the container box in Proxmox.`,
		}},
		{"a Proxmox VM with cloud-init", hostNameFacts{HostName: str(""), ProxmoxCloudInit: true}, []string{
			empty,
			`  To keep box: name the VM box in Proxmox (cloud-init sets the hostname from it), or set proxmox.cloudInit.enable = false; in hosts/box/default.nix (cloud-init then configures no network: only for a VM that gets its address by DHCP).`,
		}},
		{"an image leaving it empty", hostNameFacts{HostName: str("")}, []string{
			empty,
			`  To keep box: set networking.hostName = lib.mkOverride 49 "box"; in hosts/box/default.nix (lib among its arguments).`,
		}},
		{"another name forced", hostNameFacts{HostName: str("other")}, []string{
			`The switch won't name this machine box: its networking.hostName is "other", and piste finds this machine's host by its hostname.`,
			`  To keep box: set networking.hostName = lib.mkOverride 49 "box"; in hosts/box/default.nix (lib among its arguments).`,
		}},
	} {
		got := hostNameWarning("box", host, c.f)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
		if strings.Contains(strings.Join(got, "\n"), "mkForce") {
			t.Errorf("%s: advises mkForce: %q", c.name, got)
		}
	}
}

func str(s string) *string { return &s }

// hostNameCases are NixOS hosts whose networking.hostName the switch
// wouldn't make box, each a list of modules beside mkFlake's own
// networking.hostName = lib.mkOverride 900 "box": nixpkgs' Proxmox VM image
// (cloud-init on, its default, forces ""), its container, and a module
// forcing another name. Cloud images that leave the name to their platform
// with a mkDefault "" (EC2, GCE) keep it.
var hostNameCases = []struct {
	name    string
	modules string // a Nix list of modules
	want    hostNameFacts
}{
	{"vm", `[ ({ modulesPath, ... }: { imports = [ (modulesPath + "/virtualisation/proxmox-image.nix") ]; }) ]`,
		hostNameFacts{HostName: str(""), ProxmoxCloudInit: true}},
	{"lxc", `[ ({ modulesPath, ... }: { imports = [ (modulesPath + "/virtualisation/proxmox-lxc.nix") ]; }) ]`,
		hostNameFacts{HostName: str(""), ProxmoxLXC: true}},
	{"forced", `[ ({ lib, ... }: { networking.hostName = lib.mkForce "other"; }) ]`,
		hostNameFacts{HostName: str("other")}},
	{"ec2", `[ ({ modulesPath, ... }: { imports = [ (modulesPath + "/virtualisation/amazon-image.nix") ]; }) ]`,
		hostNameFacts{HostName: str("box")}},
	{"gce", `[ ({ modulesPath, ... }: { imports = [ (modulesPath + "/virtualisation/google-compute-config.nix") ]; }) ]`,
		hostNameFacts{HostName: str("box")}},
}

// adviceRe is the setting a hostNameWarning's last line says to make.
var adviceRe = regexp.MustCompile(`set (.+;) in hosts/box/default\.nix`)

// The warning's advice, followed, gives a host that evaluates with its
// name, against nixpkgs' own Proxmox modules: hostNameNix read through
// nixosSystem from the engine's nixpkgs; then each warning's setting
// added, as the person would, and evaluated again. And why it is never a
// mkForce: beside the Proxmox VM image's mkForce "" (cloud-init on) it
// fails the evaluation. Skipped without nix, as TestPreflightNix is.
func TestHostNameAdviceNix(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("no nix on PATH")
	}
	if testing.Short() {
		t.Skip("evaluates NixOS systems")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	eval := func(cases string) (execx.Result, error) {
		expr := `let
  nixpkgs = (builtins.getFlake ` + nixString(workspace.OverrideRef(root)) + `).inputs.nixpkgs;
  facts = ` + hostNameNix + `;
  host = ms: (nixpkgs.lib.nixosSystem { system = "x86_64-linux"; modules = ms ++ [ ({ lib, ... }: {
    networking.hostName = lib.mkOverride 900 "box";
    system.stateVersion = "26.11";
  }) ]; }).config;
in builtins.mapAttrs (_: ms: facts (host ms)) ` + cases
		return execx.Exec{}.Run(context.Background(), execx.Cmd{Argv: []string{"nix", "eval", "--json", "--impure", "--expr", expr}})
	}
	read := func(cases string) map[string]hostNameFacts {
		t.Helper()
		res, err := eval(cases)
		if err != nil {
			t.Fatalf("%v\n%s", err, res.Stderr)
		}
		var got map[string]hostNameFacts
		if err := json.Unmarshal(res.Stdout, &got); err != nil {
			t.Fatalf("%v: %s", err, res.Stdout)
		}
		return got
	}

	var before, after strings.Builder
	before.WriteString("{\n")
	after.WriteString("{\n")
	for _, c := range hostNameCases {
		fmt.Fprintf(&before, "  %s = %s;\n", c.name, c.modules)
	}
	got := read(before.String() + "}")
	advised := 0
	for _, c := range hostNameCases {
		if !reflect.DeepEqual(got[c.name], c.want) {
			t.Errorf("%s: facts %s, want %s", c.name, show(got[c.name]), show(c.want))
			continue
		}
		w := hostNameWarning("box", "hosts/box/default.nix", got[c.name])
		if c.want.HostName != nil && *c.want.HostName == "box" {
			if w != nil {
				t.Errorf("%s keeps its name, but warns: %q", c.name, w)
			}
			continue
		}
		m := adviceRe.FindStringSubmatch(w[len(w)-1])
		if m == nil {
			t.Errorf("%s: no setting in the advice %q", c.name, w[len(w)-1])
			continue
		}
		fmt.Fprintf(&after, "  %s = %s ++ [ ({ lib, ... }: { %s }) ];\n", c.name, c.modules, m[1])
		advised++
	}
	if advised == 0 {
		t.Fatal("no advice to follow")
	}
	// mkOverride 49, the advice where cloud-init isn't on, beats the VM
	// image's mkForce "" too.
	after.WriteString(`  vmOverride = ` + hostNameCases[0].modules + ` ++ [ ({ lib, ... }: { networking.hostName = lib.mkOverride 49 "box"; }) ];` + "\n")
	followed := read(after.String() + "}")
	if len(followed) != advised+1 {
		t.Errorf("%d hosts evaluated with the advice followed, want %d", len(followed), advised+1)
	}
	for name, f := range followed {
		if f.HostName == nil || *f.HostName != "box" || hostNameWarning("box", "hosts/box/default.nix", f) != nil {
			t.Errorf("%s, the advice followed: facts %s, want hostName box", name, show(f))
		}
	}

	// A mkForce beside the Proxmox VM image's (cloud-init on) doesn't build.
	res, err := eval(`{ vm = [ ({ modulesPath, lib, ... }: { imports = [ (modulesPath + "/virtualisation/proxmox-image.nix") ];
    networking.hostName = lib.mkForce "box"; }) ]; }`)
	if err == nil || !strings.Contains(string(res.Stderr), "The option `networking.hostName' has conflicting definition values") {
		t.Errorf("mkForce beside the Proxmox image evaluated (err %v): %s%s", err, res.Stdout, res.Stderr)
	}
}

func show(f hostNameFacts) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// uuidLink names this machine's root as a host's fileSystems must name it
// to be known as this machine (judgeRoot): its /dev/disk/by-uuid link, the
// one in dir that resolves to the same device; else the root as given (a
// ZFS dataset, a device with no uuid link, no such directory).
func TestUUIDLink(t *testing.T) {
	dev := t.TempDir()
	byUUID := filepath.Join(dev, "disk", "by-uuid")
	for _, d := range []string{byUUID, filepath.Join(dev, "mapper")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"vda1", "vdb1", "vdc1", "dm-0"} {
		if err := os.WriteFile(filepath.Join(dev, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for link, to := range map[string]string{
		filepath.Join(dev, "mapper", "root"): "../dm-0",
		filepath.Join(byUUID, "1111"):        "../../vdb1",
		filepath.Join(byUUID, "2222"):        "../../vda1",
		filepath.Join(byUUID, "3333"):        "../../dm-0",
	} {
		if err := os.Symlink(to, link); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ dir, dev, want string }{
		{byUUID, filepath.Join(dev, "vda1"), filepath.Join(byUUID, "2222")},
		{byUUID, filepath.Join(dev, "mapper", "root"), filepath.Join(byUUID, "3333")},
		{byUUID, filepath.Join(byUUID, "1111"), filepath.Join(byUUID, "1111")},
		{byUUID, filepath.Join(dev, "vdc1"), filepath.Join(dev, "vdc1")},
		{byUUID, "rpool/root", "rpool/root"},
		{filepath.Join(dev, "none"), filepath.Join(dev, "vda1"), filepath.Join(dev, "vda1")},
	} {
		if got := uuidLink(c.dir, c.dev); got != c.want {
			t.Errorf("uuidLink(%s, %s) = %s, want %s", c.dir, c.dev, got, c.want)
		}
	}
}

// Whether this machine is an overlay's NixOS host of the name it is being
// added under, by root filesystem: one that resolves elsewhere is another
// machine's; only a match by uuid or partition uuid is this machine's. A
// match by anything else (a label, a device name, a by-id or by-path link,
// a /dev/mapper name, a ZFS dataset: every clone of a machine has the
// same), a host that declares no root, a container or an unknown running
// root can't tell.
func TestJudgeRoot(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"vda1", "vdb1", "dm-0"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []struct{ link, to string }{
		{"disk/by-uuid/aaaa", "vda1"}, {"disk/by-partuuid/pppp", "vda1"},
		{"disk/by-label/nixos", "vda1"}, {"disk/by-partlabel/root", "vda1"},
		{"disk/by-id/virtio-root-part1", "vda1"}, {"disk/by-path/pci-0000:00:0a.0-part1", "vda1"},
		{"disk/by-uuid/cccc", "dm-0"}, {"mapper/root", "dm-0"},
	} {
		p := filepath.Join(dir, l.link)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, l.to), p); err != nil {
			t.Fatal(err)
		}
	}
	running := resolveDevice(filepath.Join(dir, "vda1"))
	at := func(rel string) string { return filepath.Join(dir, rel) }
	label := func(rel string) string {
		return "its root, " + at(rel) + ", is a label every machine made from one image has"
	}
	name := func(dev string) string {
		return "its root, " + dev + ", is a name every machine made from one image has"
	}
	for _, c := range []struct {
		name, hostRoot, running string
		container               bool
		want                    rootVerdict
		why                     string
	}{
		{"the same disk, by uuid", at("disk/by-uuid/aaaa"), running, false, rootSame, ""},
		{"the same partition, by partuuid", at("disk/by-partuuid/pppp"), running, false, rootSame, ""},
		{"the same encrypted root, by uuid", at("disk/by-uuid/cccc"), at("mapper/root"), false, rootSame, ""},
		{"the same device path", at("vda1"), running, false, rootUnknown, name(at("vda1"))},
		{"by id", at("disk/by-id/virtio-root-part1"), running, false, rootUnknown, name(at("disk/by-id/virtio-root-part1"))},
		{"by path", at("disk/by-path/pci-0000:00:0a.0-part1"), running, false, rootUnknown, name(at("disk/by-path/pci-0000:00:0a.0-part1"))},
		{"a /dev/mapper name", at("mapper/root"), at("mapper/root"), false, rootUnknown, name(at("mapper/root"))},
		{"a ZFS dataset", "rpool/root", "rpool/root", false, rootUnknown, name("rpool/root")},
		{"another disk", at("vdb1"), running, false, rootOther, ""},
		{"a disk not on this machine", at("disk/by-uuid/bbbb"), running, false, rootOther, ""},
		{"another ZFS dataset", "rpool/root", "tank/root", false, rootOther, ""},
		{"by label", at("disk/by-label/nixos"), running, false, rootUnknown, label("disk/by-label/nixos")},
		{"by partition label", at("disk/by-partlabel/root"), running, false, rootUnknown, label("disk/by-partlabel/root")},
		{"no root declared", "", running, false, rootUnknown, `its configuration sets no fileSystems."/"`},
		{"a container", at("vdb1"), running, true, rootUnknown, "this machine is a container"},
		{"the running root unknown", at("vdb1"), "", false, rootUnknown, "this machine's root filesystem is unknown"},
	} {
		v, why := judgeRoot(c.hostRoot, c.running, c.container, resolveDevice)
		if v != c.want || why != c.why {
			t.Errorf("%s: got %v %q, want %v %q", c.name, v, why, c.want, c.why)
		}
	}
}
