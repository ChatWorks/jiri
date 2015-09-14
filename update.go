// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"path/filepath"
	"time"

	"v.io/jiri/project"
	"v.io/jiri/retry"
	"v.io/jiri/tool"
	"v.io/x/lib/cmdline"
)

var (
	gcFlag       bool
	attemptsFlag int
)

func init() {
	tool.InitializeProjectFlags(&cmdUpdate.Flags)

	cmdUpdate.Flags.BoolVar(&gcFlag, "gc", false, "Garbage collect obsolete repositories.")
	cmdUpdate.Flags.IntVar(&attemptsFlag, "attempts", 1, "Number of attempts before failing.")
}

// cmdUpdate represents the "jiri update" command.
var cmdUpdate = &cmdline.Command{
	Runner: cmdline.RunnerFunc(runUpdate),
	Name:   "update",
	Short:  "Update all jiri tools and projects",
	Long: `
Updates all jiri projects, builds the latest version of jiri
tools, and installs the resulting binaries into
$JIRI_ROOT/devtools/bin. The sequence in which the individual updates
happen guarantees that we end up with a consistent set of tools and
source code.

The set of project and tools to update is describe by a
manifest. Jiri manifests are revisioned and stored in a "manifest"
repository, that is available locally in $JIRI_ROOT/.manifest. The
manifest uses the following XML schema:

 <manifest>
   <imports>
     <import name="default"/>
     ...
   </imports>
   <projects>
     <project name="release.go.jiri"
              path="release/go/src/v.io/jiri"
              protocol="git"
              name="https://vanadium.googlesource.com/release.go.jiri"
              revision="HEAD"/>
     ...
   </projects>
   <tools>
     <tool name="jiri" package="v.io/jiri"/>
     ...
   </tools>
 </manifest>

The <import> element can be used to share settings across multiple
manifests. Import names are interpreted relative to the
$JIRI_ROOT/.manifest/v2 directory. Import cycles are not allowed and
if a project or a tool is specified multiple times, the last
specification takes effect. In particular, the elements <project
name="foo" exclude="true"/> and <tool name="bar" exclude="true"/> can
be used to exclude previously included projects and tools.

The tool identifies which manifest to use using the following
algorithm. If the $JIRI_ROOT/.local_manifest file exists, then it is
used. Otherwise, the $JIRI_ROOT/.manifest/v2/<manifest>.xml file is
used, which <manifest> is the value of the -manifest command-line
flag, which defaults to "default".

NOTE: Unlike the jiri tool commands, the above manifest file format
is not an API. It is an implementation and can change without notice.
`,
}

func runUpdate(env *cmdline.Env, _ []string) error {
	ctx := tool.NewContextFromEnv(env)

	// Create a snapshot of the current state of all projects and
	// write it to the $JIRI_ROOT/.update_history folder.
	root, err := project.JiriRoot()
	if err != nil {
		return err
	}
	snapshotFile := filepath.Join(root, ".update_history", time.Now().Format(time.RFC3339))
	if err := project.CreateSnapshot(ctx, snapshotFile); err != nil {
		return err
	}

	// Update all projects to their latest version.
	// Attempt <attemptsFlag> times before failing.
	updateFn := func() error {
		return project.UpdateUniverse(ctx, gcFlag)
	}
	return retry.Function(ctx, updateFn, retry.AttemptsOpt(attemptsFlag))
}