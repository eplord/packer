// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

//go:build amd64 && (darwin || windows || linux)

package command

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-getter/v2"
	"github.com/hashicorp/packer-plugin-sdk/acctest"
	"golang.org/x/mod/sumdb/dirhash"
)

type testCaseInit struct {
	name                                  string
	setup                                 []func(*testing.T, testCaseInit)
	Meta                                  Meta
	inPluginFolder                        map[string]string
	expectedPackerConfigDirHashBeforeInit string
	inConfigFolder                        map[string]string
	packerConfigDir                       string
	packerUserFolder                      string
	want                                  int
	dirFiles                              []string
	expectedPackerConfigDirHashAfterInit  string
	moreTests                             []func(*testing.T, testCaseInit)
}

type testBuild struct {
	want int
}

func (tb testBuild) fn(t *testing.T, tc testCaseInit) {
	bc := BuildCommand{
		Meta: tc.Meta,
	}

	args := []string{tc.packerUserFolder}
	want := tb.want
	if got := bc.Run(args); got != want {
		t.Errorf("BuildCommand.Run() = %v, want %v", got, want)
	}
}

func TestInitCommand_Run(t *testing.T) {
	// These tests will try to optimise for doing the least amount of github api
	// requests whilst testing the max amount of things at once. Hopefully they
	// don't require a GH token just yet. Acc tests are run on linux, darwin and
	// windows, so requests are done 3 times.

	cfg := &configDirSingleton{map[string]string{}}

	tests := []testCaseInit{
		{
			// here we pre-write plugins with valid checksums, Packer will
			// see those as valid installations it did.
			// the directory hash before and after init should be the same,
			// that's a no-op. This also should do no GH query, so it is best
			// to always run it.
			"already-installed-no-op",
			nil,
			TestMetaFile(t),
			map[string]string{
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_darwin_amd64":                "1",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_darwin_amd64_SHA256SUM":      "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_windows_amd64.exe":           "1.exe",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_windows_amd64.exe_SHA256SUM": "07d8453027192ee0c4120242e6e84e2ca2328b8e0f506e2f818a1a5b82790a0b",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_linux_amd64":                 "1.out",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_linux_amd64_SHA256SUM":       "59031c50e0dfeedfde2b4e9445754804dce3f29e4efa737eead0ca9b4f5b85a5",
			},
			"h1:Q5qyAOdD43hL3CquQdVfaHpOYGf0UsZ/+wVA9Ry6cbA=",
			map[string]string{
				`cfg.pkr.hcl`: `
					packer {
						required_plugins {
							comment = {
								source  = "github.com/sylviamoss/comment"
								version = "v0.2.018"
							}
						}
					}`,
			},
			cfg.dir("1_pkr_config"),
			cfg.dir("1_pkr_user_folder"),
			0,
			nil,
			"h1:Q5qyAOdD43hL3CquQdVfaHpOYGf0UsZ/+wVA9Ry6cbA=",
			[]func(t *testing.T, tc testCaseInit){
				// test that a build will not work since plugins are broken for
				// this tests (they are not binaries).
				testBuild{want: 1}.fn,
			},
		},
		{
			// here we pre-write plugins with valid checksums, Packer will
			// see those as valid installations it did.
			// But because we require version 0.2.19, we will upgrade.
			"already-installed-upgrade",
			[]func(t *testing.T, tc testCaseInit){
				skipInitTestUnlessEnVar(acctest.TestEnvVar).fn,
			},
			TestMetaFile(t),
			map[string]string{
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_darwin_amd64":                "1",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_darwin_amd64_SHA256SUM":      "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_windows_amd64.exe":           "1.exe",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_windows_amd64.exe_SHA256SUM": "07d8453027192ee0c4120242e6e84e2ca2328b8e0f506e2f818a1a5b82790a0b",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_linux_amd64":                 "1.out",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_linux_amd64_SHA256SUM":       "59031c50e0dfeedfde2b4e9445754804dce3f29e4efa737eead0ca9b4f5b85a5",
			},
			"h1:Q5qyAOdD43hL3CquQdVfaHpOYGf0UsZ/+wVA9Ry6cbA=",
			map[string]string{
				`cfg.pkr.hcl`: `
					packer {
						required_plugins {
							comment = {
								source  = "github.com/sylviamoss/comment"
								version = "v0.2.019"
							}
						}
					}`,
				`source.pkr.hcl`: `
				source "null" "test" {
					communicator = "none"
				}
				`,
				`build.pkr.hcl`: `
				build {
					sources = ["source.null.test"]
					provisioner "comment" {
						comment		= "Begin ¡"
						ui			= true
						bubble_text	= true
					}
				}
				`,
			},
			cfg.dir("2_pkr_config"),
			cfg.dir("2_pkr_user_folder"),
			0,
			[]string{
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_darwin_amd64",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_darwin_amd64_SHA256SUM",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_linux_amd64",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_linux_amd64_SHA256SUM",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_windows_amd64.exe",
				"github.com/sylviamoss/comment/packer-plugin-comment_v0.2.18_x5.0_windows_amd64.exe_SHA256SUM",
				map[string]string{
					"darwin":  "github.com/sylviamoss/comment/packer-plugin-comment_v0.2.19_x5.0_darwin_amd64_SHA256SUM",
					"linux":   "github.com/sylviamoss/comment/packer-plugin-comment_v0.2.19_x5.0_linux_amd64_SHA256SUM",
					"windows": "github.com/sylviamoss/comment/packer-plugin-comment_v0.2.19_x5.0_windows_amd64.exe_SHA256SUM",
				}[runtime.GOOS],
				map[string]string{
					"darwin":  "github.com/sylviamoss/comment/packer-plugin-comment_v0.2.19_x5.0_darwin_amd64",
					"linux":   "github.com/sylviamoss/comment/packer-plugin-comment_v0.2.19_x5.0_linux_amd64",
					"windows": "github.com/sylviamoss/comment/packer-plugin-comment_v0.2.19_x5.0_windows_amd64.exe",
				}[runtime.GOOS],
			},
			map[string]string{
				"darwin":  "h1:ORwcCYUx8z/5n/QvuTJo2vrgKpfJA4AxlNg1G9/BCDI=",
				"linux":   "h1:CGym0+Nd0LEANgzqL0wx/LDjRL8bYwlpZ0HajPJo/hs=",
				"windows": "h1:ag0/C1YjP7KoEDYOiJHE0K+lhFgs0tVgjriWCXVT1fg=",
			}[runtime.GOOS],
			[]func(t *testing.T, tc testCaseInit){
				// test that a build will work as the plugin was just installed
				testBuild{want: 0}.fn,
			},
		},
		{
			"release-with-no-binary",
			[]func(t *testing.T, tc testCaseInit){
				skipInitTestUnlessEnVar(acctest.TestEnvVar).fn,
			},
			TestMetaFile(t),
			nil,
			"h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
			map[string]string{
				`cfg.pkr.hcl`: `
					packer {
						required_plugins {
							comment = {
								source  = "github.com/sylviamoss/comment"
								version = "v0.2.20"
							}
						}
					}`,
			},
			cfg.dir("3_pkr_config"),
			cfg.dir("3_pkr_user_folder"),
			1,
			nil,
			"h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
			nil,
		},
		{
			"unsupported-non-github-source-address",
			[]func(t *testing.T, tc testCaseInit){
				skipInitTestUnlessEnVar(acctest.TestEnvVar).fn,
			},
			TestMetaFile(t),
			nil,
			"h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
			map[string]string{
				`cfg.pkr.hcl`: `
					packer {
						required_plugins {
							comment = {
								source  = "example.com/sylviamoss/comment"
								version = "v0.2.19"
							}
						}
					}`,
			},
			cfg.dir("6_pkr_config"),
			cfg.dir("6_pkr_user_folder"),
			1,
			nil,
			"h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log.Printf("starting %s", tt.name)
			log.Printf("%#v", tt)
			t.Cleanup(func() {
				_ = os.RemoveAll(tt.packerConfigDir)
			})
			t.Cleanup(func() {
				_ = os.RemoveAll(tt.packerUserFolder)
			})
			t.Setenv("PACKER_CONFIG_DIR", tt.packerConfigDir)
			for _, init := range tt.setup {
				init(t, tt)
				if t.Skipped() {
					return
				}
			}
			createFiles(tt.packerConfigDir, tt.inPluginFolder)
			createFiles(tt.packerUserFolder, tt.inConfigFolder)

			hash, err := dirhash.HashDir(tt.packerConfigDir, "", dirhash.DefaultHash)
			if err != nil {
				t.Fatalf("HashDir: %v", err)
			}
			if diff := cmp.Diff(tt.expectedPackerConfigDirHashBeforeInit, hash); diff != "" {
				t.Errorf("unexpected dir hash before init: +found -expected %s", diff)
			}

			args := []string{tt.packerUserFolder}

			c := &InitCommand{
				Meta: tt.Meta,
			}

			if err := c.CoreConfig.Components.PluginConfig.Discover(); err != nil {
				t.Fatalf("Failed to discover plugins: %s", err)
			}

			c.CoreConfig.Components.PluginConfig.KnownPluginFolders = []string{tt.packerConfigDir}
			if got := c.Run(args); got != tt.want {
				t.Errorf("InitCommand.Run() = %v, want %v", got, tt.want)
			}

			if tt.dirFiles != nil {
				dirFiles, err := dirhash.DirFiles(tt.packerConfigDir, "")
				if err != nil {
					t.Fatalf("DirFiles: %v", err)
				}
				sort.Strings(tt.dirFiles)
				sort.Strings(dirFiles)
				if diff := cmp.Diff(tt.dirFiles, dirFiles); diff != "" {
					t.Errorf("found files differ: %v", diff)
				}
			}

			hash, err = dirhash.HashDir(tt.packerConfigDir, "", dirhash.DefaultHash)
			if err != nil {
				t.Fatalf("HashDir: %v", err)
			}
			if diff := cmp.Diff(tt.expectedPackerConfigDirHashAfterInit, hash); diff != "" {
				t.Errorf("unexpected dir hash after init: %s", diff)
			}

			for i, subTest := range tt.moreTests {
				t.Run(fmt.Sprintf("-subtest-%d", i), func(t *testing.T) {
					subTest(t, tt)
				})
			}
		})
	}
}

type skipInitTestUnlessEnVar string

func (key skipInitTestUnlessEnVar) fn(t *testing.T, tc testCaseInit) {
	// always run acc tests for now
	// if os.Getenv(string(key)) == "" {
	// 	t.Skipf("Acceptance test skipped unless env '%s' set", key)
	// }
}

type initTestGoGetPlugin struct {
	Src string
	Dst string
}

func (opts initTestGoGetPlugin) fn(t *testing.T, _ testCaseInit) {
	if _, err := getter.Get(context.Background(), opts.Dst, opts.Src); err != nil {
		t.Fatalf("get: %v", err)
	}
}

// TestInitCmd aims to test the init command, with output validation
func TestInitCmd(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectedCode int
		outputCheck  func(string, string) error
	}{
		{
			name: "Ensure init warns on template without required_plugin blocks",
			args: []string{
				testFixture("hcl", "build-var-in-pp.pkr.hcl"),
			},
			expectedCode: 0,
			outputCheck: func(stdout, stderr string) error {
				if !strings.Contains(stdout, "No plugins requirement found") {
					return fmt.Errorf("command should warn about plugin requirements not found, but did not")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &InitCommand{
				Meta: TestMetaFile(t),
			}

			exitCode := c.Run(tt.args)
			if exitCode != tt.expectedCode {
				t.Errorf("process exit code mismatch: expected %d, got %d",
					tt.expectedCode,
					exitCode)
			}

			out, stderr := GetStdoutAndErrFromTestMeta(t, c.Meta)
			err := tt.outputCheck(out, stderr)
			if err != nil {
				if len(out) != 0 {
					t.Logf("command stdout: %q", out)
				}

				if len(stderr) != 0 {
					t.Logf("command stderr: %q", stderr)
				}
				t.Error(err.Error())
			}
		})
	}
}
