// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package packer

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
	pluginsdk "github.com/hashicorp/packer-plugin-sdk/plugin"
	plugingetter "github.com/hashicorp/packer/packer/plugin-getter"
)

var defaultChecksummer = plugingetter.Checksummer{
	Type: "sha256",
	Hash: sha256.New(),
}

// PluginConfig helps load and use packer plugins
type PluginConfig struct {
	KnownPluginFolders []string
	PluginMinPort      int
	PluginMaxPort      int
	Builders           BuilderSet
	Provisioners       ProvisionerSet
	PostProcessors     PostProcessorSet
	DataSources        DatasourceSet
}

// PACKERSPACE is used to represent the spaces that separate args for a command
// without being confused with spaces in the path to the command itself.
const PACKERSPACE = "-PACKERSPACE-"

// Discover discovers plugins.
//
// Search the directory of the executable, then the plugins directory, and
// finally the CWD, in that order. Any conflicts will overwrite previously
// found plugins, in that order.
// Hence, the priority order is the reverse of the search order - i.e., the
// CWD has the highest priority.
func (c *PluginConfig) Discover() error {
	if c.Builders == nil {
		c.Builders = MapOfBuilder{}
	}
	if c.Provisioners == nil {
		c.Provisioners = MapOfProvisioner{}
	}
	if c.PostProcessors == nil {
		c.PostProcessors = MapOfPostProcessor{}
	}
	if c.DataSources == nil {
		c.DataSources = MapOfDatasource{}
	}

	// If we are already inside a plugin process we should not need to
	// discover anything.
	if os.Getenv(pluginsdk.MagicCookieKey) == pluginsdk.MagicCookieValue {
		return nil
	}

	if len(c.KnownPluginFolders) == 0 {
		//PluginFolders should match the call in github.com/hahicorp/packer/main.go#loadConfig
		c.KnownPluginFolders = PluginFolders(".")
	}

	// Pick last folder as it's the one with the highest priority
	// This is the same logic used when installing plugins via Packer's plugin installation commands.
	pluginInstallationPath := c.KnownPluginFolders[len(c.KnownPluginFolders)-1]
	if err := c.discoverInstalledComponents(pluginInstallationPath); err != nil {
		return err
	}

	// Manually installed plugins take precedence over all. Duplicate plugins installed
	// prior to the packer plugins install command should be removed by user to avoid overrides.
	for _, knownFolder := range c.KnownPluginFolders {
		pluginPaths, err := c.discoverSingle(filepath.Join(knownFolder, "packer-plugin-*"))
		if err != nil {
			return err
		}
		for pluginName, pluginPath := range pluginPaths {
			// Test pluginPath points to an executable
			if _, err := exec.LookPath(pluginPath); err != nil {
				log.Printf("[WARN] %q is not executable; skipping", pluginPath)
				continue
			}
			if err := c.DiscoverMultiPlugin(pluginName, pluginPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *PluginConfig) discoverSingle(glob string) (map[string]string, error) {
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	var prefix string
	res := make(map[string]string)
	// Sort the matches so we add the newer version of a plugin last
	sort.Strings(matches)
	prefix = filepath.Base(glob)
	prefix = prefix[:strings.Index(prefix, "*")]
	for _, match := range matches {
		file := filepath.Base(match)
		// skip folders like packer-plugin-sdk
		if stat, err := os.Stat(file); err == nil && stat.IsDir() {
			continue
		}

		// On Windows, ignore any plugins that don't end in .exe.
		// We could do a full PATHEXT parse, but this is probably good enough.
		if runtime.GOOS == "windows" && strings.ToLower(filepath.Ext(file)) != ".exe" {
			log.Printf(
				"[TRACE] Ignoring plugin match %s, no exe extension",
				match)
			continue
		}

		if strings.Contains(strings.ToUpper(file), defaultChecksummer.FileExt()) {
			log.Printf(
				"[TRACE] Ignoring plugin match %s, which looks to be a checksum file",
				match)
			continue

		}

		// If the filename has a ".", trim up to there
		if idx := strings.Index(file, ".exe"); idx >= 0 {
			file = file[:idx]
		}

		// Look for foo-bar-baz. The plugin name is "baz"
		pluginName := file[len(prefix):]
		// multi-component plugins installed via the plugins subcommand will have a name that looks like baz_vx.y.z_x5.0_darwin_arm64.
		// After the split the plugin name is "baz".
		pluginName = strings.SplitN(pluginName, "_", 2)[0]

		log.Printf("[INFO] Discovered potential plugin: %s = %s", pluginName, match)
		pluginPath, err := filepath.Abs(match)
		if err != nil {
			pluginPath = match
		}
		res[pluginName] = pluginPath
	}

	return res, nil
}

// DiscoverMultiPlugin takes the description from a multi-component plugin
// binary and makes the plugins available to use in Packer. Each plugin found in the
// binary will be addressable using `${pluginName}-${builderName}` for example.
// pluginName could be manually set. It usually is a cloud name like amazon.
// pluginName can be extrapolated from the filename of the binary; so
// if the "packer-plugin-amazon" binary had an "ebs" builder one could use
// the "amazon-ebs" builder.
func (c *PluginConfig) DiscoverMultiPlugin(pluginName, pluginPath string) error {
	out, err := exec.Command(pluginPath, "describe").Output()
	if err != nil {
		return err
	}
	var desc pluginsdk.SetDescription
	if err := json.Unmarshal(out, &desc); err != nil {
		return err
	}

	pluginPrefix := pluginName + "-"

	for _, builderName := range desc.Builders {
		builderName := builderName // copy to avoid pointer overwrite issue
		key := pluginPrefix + builderName
		if builderName == pluginsdk.DEFAULT_NAME {
			key = pluginName
		}
		c.Builders.Set(key, func() (packersdk.Builder, error) {
			return c.Client(pluginPath, "start", "builder", builderName).Builder()
		})
	}

	if len(desc.Builders) > 0 {
		log.Printf("[INFO] found external %v builders from %s plugin", desc.Builders, pluginName)
	}

	for _, postProcessorName := range desc.PostProcessors {
		postProcessorName := postProcessorName // copy to avoid pointer overwrite issue
		key := pluginPrefix + postProcessorName
		if postProcessorName == pluginsdk.DEFAULT_NAME {
			key = pluginName
		}
		c.PostProcessors.Set(key, func() (packersdk.PostProcessor, error) {
			return c.Client(pluginPath, "start", "post-processor", postProcessorName).PostProcessor()
		})
	}

	if len(desc.PostProcessors) > 0 {
		log.Printf("[INFO] found external %v post-processors from %s plugin", desc.PostProcessors, pluginName)
	}

	for _, provisionerName := range desc.Provisioners {
		provisionerName := provisionerName // copy to avoid pointer overwrite issue
		key := pluginPrefix + provisionerName
		if provisionerName == pluginsdk.DEFAULT_NAME {
			key = pluginName
		}
		c.Provisioners.Set(key, func() (packersdk.Provisioner, error) {
			return c.Client(pluginPath, "start", "provisioner", provisionerName).Provisioner()
		})
	}
	if len(desc.Provisioners) > 0 {
		log.Printf("found external %v provisioner from %s plugin", desc.Provisioners, pluginName)
	}

	for _, datasourceName := range desc.Datasources {
		datasourceName := datasourceName // copy to avoid pointer overwrite issue
		key := pluginPrefix + datasourceName
		if datasourceName == pluginsdk.DEFAULT_NAME {
			key = pluginName
		}
		c.DataSources.Set(key, func() (packersdk.Datasource, error) {
			return c.Client(pluginPath, "start", "datasource", datasourceName).Datasource()
		})
	}
	if len(desc.Datasources) > 0 {
		log.Printf("found external %v datasource from %s plugin", desc.Datasources, pluginName)
	}

	return nil
}

func (c *PluginConfig) Client(path string, args ...string) *PluginClient {
	originalPath := path

	// Check for special case using `packer plugin PLUGIN`
	if strings.Contains(path, PACKERSPACE) {
		parts := strings.Split(path, PACKERSPACE)
		path = parts[0]
		args = parts[1:]
	}

	// First attempt to find the executable by consulting the PATH.
	path, err := exec.LookPath(path)
	if err != nil {
		// If that doesn't work, look for it in the same directory
		// as the `packer` executable (us).
		log.Printf("[INFO] exec.LookPath: %s : %v. Checking same directory as executable.", path, err)
		exePath, err := os.Executable()
		if err != nil {
			log.Printf("Couldn't get current exe path: %s", err)
		} else {
			log.Printf("Current exe path: %s", exePath)
			path = filepath.Join(filepath.Dir(exePath), filepath.Base(originalPath))
		}
	}

	// If everything failed, just use the original path and let the error
	// bubble through.
	if path == "" {
		path = originalPath
	}

	if strings.Contains(originalPath, PACKERSPACE) {
		log.Printf("[INFO] Starting internal plugin %s", args[len(args)-1])
	} else {
		log.Printf("[INFO] Starting external plugin %s %s", path, strings.Join(args, " "))
	}
	var config PluginClientConfig
	config.Cmd = exec.Command(path, args...)
	config.Managed = true
	config.MinPort = c.PluginMinPort
	config.MaxPort = c.PluginMaxPort
	return NewClient(&config)
}

// discoverInstalledComponents scans the provided path for plugins installed by running packer plugins install or packer init.
// Valid plugins contain a matching system binary and valid checksum file.
func (c *PluginConfig) discoverInstalledComponents(path string) error {
	//Check for installed plugins using the `packer plugins install` command
	binInstallOpts := plugingetter.BinaryInstallationOptions{
		OS:              runtime.GOOS,
		ARCH:            runtime.GOARCH,
		APIVersionMajor: pluginsdk.APIVersionMajor,
		APIVersionMinor: pluginsdk.APIVersionMinor,
		Checksummers: []plugingetter.Checksummer{
			defaultChecksummer,
		},
	}

	if runtime.GOOS == "windows" {
		binInstallOpts.Ext = ".exe"
	}

	pluginPath := filepath.Join(path, "*", "*", "*", fmt.Sprintf("packer-plugin-*%s", binInstallOpts.FilenameSuffix()))
	pluginPaths, err := c.discoverSingle(pluginPath)
	if err != nil {
		return err
	}

	for pluginName, pluginPath := range pluginPaths {
		var checksumOk bool
		for _, checksummer := range binInstallOpts.Checksummers {
			cs, err := checksummer.GetCacheChecksumOfFile(pluginPath)
			if err != nil {
				log.Printf("[TRACE] GetChecksumOfFile(%q) failed: %v", pluginPath, err)
				continue
			}

			if err := checksummer.ChecksumFile(cs, pluginPath); err != nil {
				log.Printf("[TRACE] ChecksumFile(%q) failed: %v", pluginPath, err)
				continue
			}
			checksumOk = true
			break
		}

		if !checksumOk {
			log.Printf("[WARN] No checksum found for %q ignoring possibly unsafe binary", path)
			continue
		}

		if err := c.DiscoverMultiPlugin(pluginName, pluginPath); err != nil {
			return err
		}
	}

	return nil
}
