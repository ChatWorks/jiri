// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package project_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"fuchsia.googlesource.com/jiri"
	"fuchsia.googlesource.com/jiri/git"
	"fuchsia.googlesource.com/jiri/gitutil"
	"fuchsia.googlesource.com/jiri/jiritest"
	"fuchsia.googlesource.com/jiri/project"
)

func dirExists(dirname string) error {
	fileInfo, err := os.Stat(dirname)
	if err != nil {
		return err
	}
	if !fileInfo.IsDir() {
		return os.ErrNotExist
	}
	return nil
}

func fileExists(dirname string) error {
	fileInfo, err := os.Stat(dirname)
	if err != nil {
		return err
	}
	if fileInfo.IsDir() {
		return os.ErrNotExist
	}
	return nil
}

func checkReadme(t *testing.T, jirix *jiri.X, p project.Project, message string) {
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatalf("%v", err)
	}
	readmeFile := filepath.Join(p.Path, "README")
	data, err := ioutil.ReadFile(readmeFile)
	if err != nil {
		t.Fatalf("ReadFile(%v) failed: %v", readmeFile, err)
	}
	if got, want := data, []byte(message); bytes.Compare(got, want) != 0 {
		t.Fatalf("unexpected content in project %v:\ngot\n%s\nwant\n%s\n", p.Name, got, want)
	}
}

func checkJiriRevFiles(t *testing.T, jirix *jiri.X, p project.Project) {
	g := git.NewGit(p.Path)

	file := filepath.Join(p.Path, ".git", "JIRI_HEAD")
	data, err := ioutil.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile(%v) failed: %s", file, err)
	}
	headFileContents := string(data)
	headFileCommit, err := g.CurrentRevisionForRef(headFileContents)
	if err != nil {
		t.Fatalf("CurrentRevisionForRef failed: %s", err)
	}

	projectRevision := p.Revision
	if projectRevision == "" {
		if p.RemoteBranch == "" {
			projectRevision = "origin/master"
		} else {
			projectRevision = "origin/" + p.RemoteBranch
		}
	}
	revisionCommit, err := g.CurrentRevisionForRef(projectRevision)
	if err != nil {
		t.Fatalf("CurrentRevisionForRef failed: %s", err)
	}

	if revisionCommit != headFileCommit {
		t.Fatalf("JIRI_HEAD contains %s (%s) expected %s (%s)", headFileContents, headFileCommit, projectRevision, revisionCommit)
	}
	file = filepath.Join(p.Path, ".git", "JIRI_LAST_BASE")
	data, err = ioutil.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile(%v) failed: %s", file, err)
	}
	if rev, err := g.CurrentRevision(); err != nil {
		t.Fatalf("CurrentRevision() failed: %s", err)
	} else if rev != string(data) {
		t.Fatalf("JIRI_LAST_BASE contains %s expected %s", string(data), rev)
	}
}

// Checks that /.jiri/ is ignored in a local project checkout
func checkMetadataIsIgnored(t *testing.T, jirix *jiri.X, p project.Project) {
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatalf("%v", err)
	}
	gitInfoExcludeFile := filepath.Join(p.Path, ".git", "info", "exclude")
	data, err := ioutil.ReadFile(gitInfoExcludeFile)
	if err != nil {
		t.Fatalf("ReadFile(%v) failed: %v", gitInfoExcludeFile, err)
	}
	excludeString := "/.jiri/"
	if !strings.Contains(string(data), excludeString) {
		t.Fatalf("Did not find \"%v\" in exclude file", excludeString)
	}
}

func commitFile(t *testing.T, jirix *jiri.X, dir, file, msg string) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := gitutil.New(jirix, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com")).CommitFile(file, msg); err != nil {
		t.Fatal(err)
	}
}

func projectName(i int) string {
	return fmt.Sprintf("project-%d", i)
}

func writeUncommitedFile(t *testing.T, jirix *jiri.X, projectDir, fileName, message string) string {
	path, perm := filepath.Join(projectDir, fileName), os.FileMode(0644)
	if err := ioutil.WriteFile(path, []byte(message), perm); err != nil {
		t.Fatalf("WriteFile(%v, %v) failed: %v", path, perm, err)
	}
	return path
}
func writeFile(t *testing.T, jirix *jiri.X, projectDir, fileName, message string) {
	path := writeUncommitedFile(t, jirix, projectDir, fileName, message)
	commitFile(t, jirix, projectDir, path, "creating "+fileName)
}

func writeReadme(t *testing.T, jirix *jiri.X, projectDir, message string) {
	writeFile(t, jirix, projectDir, "README", message)
}

func checkProjectsMatchPaths(t *testing.T, gotProjects project.Projects, wantProjectPaths []string) {
	gotProjectPaths := []string{}
	for _, p := range gotProjects {
		gotProjectPaths = append(gotProjectPaths, p.Path)
	}
	sort.Strings(gotProjectPaths)
	sort.Strings(wantProjectPaths)
	if !reflect.DeepEqual(gotProjectPaths, wantProjectPaths) {
		t.Errorf("project paths got %v, want %v", gotProjectPaths, wantProjectPaths)
	}
}

// TestLocalProjects tests the behavior of the LocalProjects method with
// different ScanModes.
func TestLocalProjects(t *testing.T) {
	jirix, cleanup := jiritest.NewX(t)
	defer cleanup()

	// Create some projects.
	numProjects, projectPaths := 3, []string{}
	for i := 0; i < numProjects; i++ {
		name := projectName(i)
		path := filepath.Join(jirix.Root, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}

		// Initialize empty git repository.  The commit is necessary, otherwise
		// "git rev-parse master" fails.
		git := gitutil.New(jirix, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(path))
		if err := git.Init(path); err != nil {
			t.Fatal(err)
		}
		if err := git.Commit(); err != nil {
			t.Fatal(err)
		}

		// Write project metadata.
		p := project.Project{
			Path: path,
			Name: name,
		}
		if err := project.InternalWriteMetadata(jirix, p, path); err != nil {
			t.Fatalf("writeMetadata %v %v) failed: %v\n", p, path, err)
		}
		projectPaths = append(projectPaths, path)
	}

	// Create a latest update snapshot but only tell it about the first project.
	manifest := project.Manifest{
		Projects: []project.Project{
			{
				Name: projectName(0),
				Path: projectPaths[0],
			},
		},
	}
	if err := os.MkdirAll(jirix.UpdateHistoryDir(), 0755); err != nil {
		t.Fatalf("MkdirAll(%v) failed: %v", jirix.UpdateHistoryDir(), err)
	}
	if err := manifest.ToFile(jirix, jirix.UpdateHistoryLatestLink()); err != nil {
		t.Fatalf("manifest.ToFile(%v) failed: %v", jirix.UpdateHistoryLatestLink(), err)
	}

	// LocalProjects with scanMode = FastScan should only find the first
	// project.
	foundProjects, err := project.LocalProjects(jirix, project.FastScan)
	if err != nil {
		t.Fatalf("LocalProjects(%v) failed: %v", project.FastScan, err)
	}
	checkProjectsMatchPaths(t, foundProjects, projectPaths[:1])

	// LocalProjects with scanMode = FullScan should find all projects.
	foundProjects, err = project.LocalProjects(jirix, project.FullScan)
	if err != nil {
		t.Fatalf("LocalProjects(%v) failed: %v", project.FastScan, err)
	}
	checkProjectsMatchPaths(t, foundProjects, projectPaths[:])

	// Check that deleting a project forces LocalProjects to run a full scan,
	// even if FastScan is specified.
	if err := os.RemoveAll(projectPaths[0]); err != nil {
		t.Fatalf("RemoveAll(%v) failed: %v", projectPaths[0])
	}
	foundProjects, err = project.LocalProjects(jirix, project.FastScan)
	if err != nil {
		t.Fatalf("LocalProjects(%v) failed: %v", project.FastScan, err)
	}
	checkProjectsMatchPaths(t, foundProjects, projectPaths[1:])
}

// setupUniverse creates a fake jiri root with 3 remote projects.  Each project
// has a README with text "initial readme".
func setupUniverse(t *testing.T) ([]project.Project, *jiritest.FakeJiriRoot, func()) {
	fake, cleanup := jiritest.NewFakeJiriRoot(t)
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()

	// Create some projects and add them to the remote manifest.
	numProjects := 7
	localProjects := []project.Project{}
	for i := 0; i < numProjects; i++ {
		name := projectName(i)
		path := fmt.Sprintf("path-%d", i)
		if err := fake.CreateRemoteProject(name); err != nil {
			t.Fatal(err)
		}
		p := project.Project{
			Name:   name,
			Path:   filepath.Join(fake.X.Root, path),
			Remote: fake.Projects[name],
		}
		localProjects = append(localProjects, p)
	}
	localProjects[2].HistoryDepth = 1
	localProjects[3].Path = filepath.Join(localProjects[2].Path, "path-3")
	localProjects[4].Path = filepath.Join(localProjects[3].Path, "path-4")
	localProjects[5].Path = filepath.Join(localProjects[2].Path, "path-5")
	localProjects[6].Path = filepath.Join(localProjects[0].Path, "path-6")
	for _, p := range localProjects {
		if err := fake.AddProject(p); err != nil {
			t.Fatal(err)
		}
	}

	// Create initial commit in each repo.
	for _, remoteProjectDir := range fake.Projects {
		writeReadme(t, fake.X, remoteProjectDir, "initial readme")
	}
	writeFile(t, fake.X, fake.Projects[localProjects[2].Name], ".gitignore", "path-3/\npath-5/\n")
	writeFile(t, fake.X, fake.Projects[localProjects[0].Name], ".gitignore", "path-6/\n")
	writeFile(t, fake.X, fake.Projects[localProjects[3].Name], ".gitignore", "path-4/\n")

	success = true
	return localProjects, fake, cleanup
}

// TestUpdateUniverseSimple tests that UpdateUniverse will pull remote projects
// locally, and that jiri metadata is ignored in the repos.
func TestUpdateUniverseSimple(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	// Check that calling UpdateUniverse() creates local copies of the remote
	// repositories, and that jiri metadata is ignored by git.
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	for _, p := range localProjects {
		if err := dirExists(p.Path); err != nil {
			t.Fatalf("expected project to exist at path %q but none found", p.Path)
		}
		if branches, _, err := git.NewGit(p.Path).GetBranches(); err != nil {
			t.Fatal(err)
		} else if len(branches) != 0 {
			t.Fatalf("expected project %s(%s) to contain no branches but it contains %s", p.Name, p.Path, branches)
		}
		checkReadme(t, fake.X, p, "initial readme")
		checkMetadataIsIgnored(t, fake.X, p)
		checkJiriRevFiles(t, fake.X, p)
	}
}

// TestUpdateUniverseWithCache checks that UpdateUniverse can clone and pull
// from a cache.
func testWithCache(t *testing.T, shared bool) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	// Create cache directory
	cacheDir, err := ioutil.TempDir("", "cache")
	if err != nil {
		t.Fatalf("TempDir() failed: %v", err)
	}
	if err := os.MkdirAll(cacheDir, os.FileMode(0700)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(cacheDir); err != nil {
			t.Fatalf("RemoveAll(%q) failed: %v", cacheDir, err)
		}
	}()
	fake.X.Cache = cacheDir
	fake.X.Shared = shared

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	for _, p := range localProjects {
		// Check that local clone was referenced from cache
		err := fileExists(p.Path + "/.git/objects/info/alternates")
		if shared || p.HistoryDepth == 0 {
			if err != nil {
				t.Fatalf("expected %v to exist, but not found", p.Path+"/.git/objects/info/alternates")
			}
		} else if err == nil {
			t.Fatalf("expected %v to not exist, but found", p.Path+"/.git/objects/info/alternates")
		}
		checkReadme(t, fake.X, p, "initial readme")
		checkJiriRevFiles(t, fake.X, p)
	}

	// Commit to master branch of a project 1.
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	checkReadme(t, fake.X, localProjects[1], "master commit")
	checkJiriRevFiles(t, fake.X, localProjects[1])

	// Check that cache was updated
	cacheDirPath, err := localProjects[1].CacheDirPath(fake.X)
	if err != nil {
		t.Fatal(err)
	}
	gCache := git.NewGit(cacheDirPath)
	cacheRev, err := gCache.CurrentRevision()
	if err != nil {
		t.Fatal(err)
	}
	gl := git.NewGit(localProjects[1].Path)
	localRev, err := gl.CurrentRevision()
	if err != nil {
		t.Fatal(err)
	}
	if cacheRev != localRev {
		t.Fatalf("Cache revision(%v) not equal to local revision(%v)", cacheRev, localRev)
	}

}

// TestUpdateUniverseWithCache checks that UpdateUniverse can clone and pull
// from a cache.
func TestUpdateUniverseWithCache(t *testing.T) {
	testWithCache(t, false)
}

// TestUpdateUniverseWithiSharedCache checks that UpdateUniverse can clone and pull
// from a cache when it is of type "shared"
func TestUpdateUniverseWithSharedCache(t *testing.T) {
	testWithCache(t, true)
}

func TestProjectUpdateWhenNoUpdate(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	lc := project.LocalConfig{NoUpdate: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	// Commit to master branch of a project 1.
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")
	gitRemote := git.NewGit(fake.Projects[localProjects[1].Name])
	remoteRev, _ := gitRemote.CurrentRevision()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gitLocal := git.NewGit(localProjects[1].Path)
	localRev, _ := gitLocal.CurrentRevision()

	if remoteRev == localRev {
		t.Fatal("local project should not be updated")
	}
}

func TestProjectUpdateWhenIgnore(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	lc := project.LocalConfig{Ignore: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	// Commit to master branch of a project 1.
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")
	gitRemote := git.NewGit(fake.Projects[localProjects[1].Name])
	remoteRev, _ := gitRemote.CurrentRevision()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gitLocal := git.NewGit(localProjects[1].Path)
	localRev, _ := gitLocal.CurrentRevision()

	if remoteRev == localRev {
		t.Fatal("local project should not be updated")
	}
}

func TestLocalProjectWithConfig(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	project.WriteUpdateHistorySnapshot(fake.X, "", false)

	lc := project.LocalConfig{Ignore: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	scanModes := []project.ScanMode{project.FullScan, project.FastScan}
	for _, scanMode := range scanModes {
		newLocalProjects, err := project.LocalProjects(fake.X, scanMode)
		if err != nil {
			t.Fatal(err)
		}
		for k, p := range newLocalProjects {
			expectedIgnore := k == localProjects[1].Key()
			if p.LocalConfig.Ignore != expectedIgnore {
				t.Errorf("local config ignore: got %t, want %t", p.LocalConfig.Ignore, expectedIgnore)
			}

			if p.LocalConfig.NoUpdate != false {
				t.Errorf("local config no-update: got %t, want %t", p.LocalConfig.NoUpdate, false)
			}

			if p.LocalConfig.NoRebase != false {
				t.Errorf("local config no-rebase: got %t, want %t", p.LocalConfig.NoUpdate, false)
			}
		}
	}
}

func TestProjectUpdateWhenNoRebase(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	lc := project.LocalConfig{NoRebase: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	// Commit to master branch of a project 1.
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")
	gitRemote := git.NewGit(fake.Projects[localProjects[1].Name])
	remoteRev, _ := gitRemote.CurrentRevision()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gitLocal := git.NewGit(localProjects[1].Path)
	localRev, _ := gitLocal.CurrentRevision()

	if remoteRev != localRev {
		t.Fatal("local project should be updated")
	}
}

func TestBranchUpdateWhenNoRebase(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	gitLocal := gitutil.New(fake.X, gitutil.RootDirOpt(localProjects[1].Path))
	gitLocal.CheckoutBranch("master")

	lc := project.LocalConfig{NoRebase: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	// Commit to master branch of a project 1.
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")
	gitRemote := git.NewGit(fake.Projects[localProjects[1].Name])
	remoteRev, _ := gitRemote.CurrentRevision()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gl := git.NewGit(localProjects[1].Path)
	localRev, _ := gl.CurrentRevision()

	if remoteRev == localRev {
		t.Fatal("local branch master should not be updated")
	}
}

// TestHookLoadSimple tests that manifest is loaded correctly
// with correct project path in hook
func TestHookLoadSimple(t *testing.T) {
	p, fake, cleanup := setupUniverse(t)
	defer cleanup()
	err := fake.AddHook(project.Hook{Name: "hook1",
		Action:      "action.sh",
		ProjectName: p[0].Name})

	if err != nil {
		t.Fatal(err)
	}
	err = fake.UpdateUniverse(false)
	if err == nil {
		t.Fatal("run hook should throw error as there is no action.sh script")
	}
	if !strings.Contains(err.Error(), "no such file or directory") || !strings.Contains(err.Error(), "action.sh") {
		t.Fatal(err)
	}
}

// TestHookLoadError tests that manifest load
// throws error for invalid hook
func TestHookLoadError(t *testing.T) {
	_, fake, cleanup := setupUniverse(t)
	defer cleanup()
	err := fake.AddHook(project.Hook{Name: "hook1",
		Action:      "action",
		ProjectName: "non-existant"})

	if err != nil {
		t.Fatal(err)
	}
	err = fake.UpdateUniverse(false)
	if err == nil {
		t.Fatal("Update universe should throw error for the hook")
	}
	if !strings.Contains(err.Error(), "invalid hook") {
		t.Fatal(err)
	}
}

// TestJiriExcludeForRepoUpdate tests that .git/info/exclude contains
// /.jiri/ after every update
func TestJiriExcludeForRepoUpdate(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	p := localProjects[0]
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatalf("%v", err)
	}
	gitInfoExcludeFile := filepath.Join(p.Path, ".git", "info", "exclude")

	// Test when exclude doesn't exist
	if err := os.RemoveAll(gitInfoExcludeFile); err != nil {
		t.Fatalf("%v", err)
	}
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	checkMetadataIsIgnored(t, fake.X, p)

	// Check when exclude doesn't have /.jiri/
	if err := ioutil.WriteFile(gitInfoExcludeFile, []byte(""), 0644); err != nil {
		t.Fatalf("%v", err)
	}
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	checkMetadataIsIgnored(t, fake.X, p)
}

// TestUpdateUniverseWithRevision checks that UpdateUniverse will pull remote
// projects at the specified revision.
func TestUpdateUniverseWithRevision(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	// Set project 1's revision in the manifest to the current revision.
	g := git.NewGit(fake.Projects[localProjects[1].Name])
	rev, err := g.CurrentRevision()
	if err != nil {
		t.Fatal(err)
	}
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Name == localProjects[1].Name {
			p.Revision = rev
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}
	// Update README in all projects.
	for _, remoteProjectDir := range fake.Projects {
		writeReadme(t, fake.X, remoteProjectDir, "new revision")
	}
	// Check that calling UpdateUniverse() updates all projects except for
	// project 1.
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	for i, p := range localProjects {
		if i == 1 {
			checkReadme(t, fake.X, p, "initial readme")
		} else {
			checkReadme(t, fake.X, p, "new revision")
		}
	}
}

func commitChanges(t *testing.T, jirix *jiri.X, dir string) {
	scm := gitutil.New(jirix, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(dir))
	if err := scm.AddUpdatedFiles(); err != nil {
		t.Fatal(err)
	}
	if err := scm.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestSubDirToNestedProj checks that UpdateUniverse will correctly update when
// nested folder is converted to nested project
func TestSubDirToNestedProj(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	folderName := "nested_folder"
	nestedFolderPath := filepath.Join(fake.Projects[localProjects[1].Name], folderName)
	os.MkdirAll(nestedFolderPath, os.FileMode(0755))
	writeReadme(t, fake.X, nestedFolderPath, "nested folder")

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(nestedFolderPath)
	commitChanges(t, fake.X, fake.Projects[localProjects[1].Name])

	// Create nested project
	if err := fake.CreateRemoteProject(folderName); err != nil {
		t.Fatal(err)
	}
	writeReadme(t, fake.X, fake.Projects[folderName], "nested folder")
	p := project.Project{
		Name:   folderName,
		Path:   filepath.Join(localProjects[1].Path, folderName),
		Remote: fake.Projects[folderName],
	}
	if err := fake.AddProject(p); err != nil {
		t.Fatal(err)
	}

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	checkReadme(t, fake.X, p, "nested folder")
}

// TestMoveNestedProjects checks that UpdateUniverse will correctly move nested projects
func TestMoveNestedProjects(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()

	folderName := "nested_proj"
	// Create nested project
	if err := fake.CreateRemoteProject(folderName); err != nil {
		t.Fatal(err)
	}
	writeReadme(t, fake.X, fake.Projects[folderName], "nested folder")
	p := project.Project{
		Name:   folderName,
		Path:   filepath.Join(localProjects[1].Path, folderName),
		Remote: fake.Projects[folderName],
	}
	if err := fake.AddProject(p); err != nil {
		t.Fatal(err)
	}

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	oldProjectPath := localProjects[1].Path
	localProjects[1].Path = filepath.Join(fake.X.Root, "new-project-path")
	p.Path = filepath.Join(localProjects[1].Path, folderName)
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	projects := []project.Project{}
	for _, proj := range m.Projects {
		if proj.Name == localProjects[1].Name {
			proj.Path = localProjects[1].Path
		}
		if proj.Name == p.Name {
			proj.Path = p.Path
		}
		projects = append(projects, proj)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	checkReadme(t, fake.X, localProjects[1], "initial readme")
	checkReadme(t, fake.X, p, "nested folder")
	if err := dirExists(oldProjectPath); err == nil {
		t.Fatalf("expected project %q at path %q not to exist but it did", localProjects[1].Name, oldProjectPath)
	}
}

// TestUpdateUniverseWithUncommitted checks that uncommitted files are not droped
// by UpdateUniverse(). This ensures that the "git reset --hard" mechanism used
// for pointing the master branch to a fixed revision does not lose work in
// progress.
func TestUpdateUniverseWithUncommitted(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Create an uncommitted file in project 1.
	file, perm, want := filepath.Join(localProjects[1].Path, "uncommitted_file"), os.FileMode(0644), []byte("uncommitted work")
	if err := ioutil.WriteFile(file, want, perm); err != nil {
		t.Fatalf("WriteFile(%v, %v) failed: %v", file, err, perm)
	}
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	got, err := ioutil.ReadFile(file)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if bytes.Compare(got, want) != 0 {
		t.Fatalf("unexpected content %v:\ngot\n%s\nwant\n%s\n", localProjects[1], got, want)
	}
}

// TestUpdateUniverseMovedProject checks that UpdateUniverse can move a
// project.
func TestUpdateUniverseMovedProject(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Update the local path at which project 1 is located.
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	oldProjectPath := localProjects[1].Path
	localProjects[1].Path = filepath.Join(fake.X.Root, "new-project-path")
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Name == localProjects[1].Name {
			p.Path = localProjects[1].Path
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}
	// Check that UpdateUniverse() moves the local copy of the project 1.
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	if err := dirExists(oldProjectPath); err == nil {
		t.Fatalf("expected project %q at path %q not to exist but it did", localProjects[1].Name, oldProjectPath)
	}
	if err := dirExists(localProjects[2].Path); err != nil {
		t.Fatalf("expected project %q at path %q to exist but it did not", localProjects[1].Name, localProjects[1].Path)
	}
	checkReadme(t, fake.X, localProjects[1], "initial readme")
}

func TestIgnoredProjectsNotMoved(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Update the local path at which project 1 is located.
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	lc := project.LocalConfig{Ignore: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	oldProjectPath := localProjects[1].Path
	localProjects[1].Path = filepath.Join(fake.X.Root, "new-project-path")
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Name == localProjects[1].Name {
			p.Path = localProjects[1].Path
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}

	// Check that UpdateUniverse() does not move the local copy of the project 1.
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	if err := dirExists(oldProjectPath); err != nil {
		t.Fatalf("expected project %q at path %q to exist but it did not: %s", localProjects[1].Name, oldProjectPath, err)
	}
	if err := dirExists(localProjects[2].Path); err != nil {
		t.Fatalf("expected project %q at path %q to not exist but it did", localProjects[1].Name, localProjects[1].Path)
	}
}

// TestUpdateUniverseRenamedProject checks that UpdateUniverse can update
// renamed project.
func TestUpdateUniverseRenamedProject(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	oldProjectName := localProjects[1].Name
	localProjects[1].Name = localProjects[1].Name + "new"
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Name == oldProjectName {
			p.Name = localProjects[1].Name
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	newLocalProjects, err := project.LocalProjects(fake.X, project.FullScan)
	if err != nil {
		t.Fatal(err)
	}
	projectFound := false
	for _, p := range newLocalProjects {
		if p.Name == localProjects[1].Name {
			projectFound = true
		}
	}
	if !projectFound {
		t.Fatalf("Project with updated name(%v) not found", localProjects[1].Name)
	}
}

// testUpdateUniverseDeletedProject checks that UpdateUniverse will delete a
// project if gc=true.
func testUpdateUniverseDeletedProject(t *testing.T, testDirtyProjectDelete bool) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Delete project 1.
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	projects := []project.Project{}
	if testDirtyProjectDelete {
		writeUncommitedFile(t, fake.X, localProjects[4].Path, "extra", "")
	}
	for _, p := range m.Projects {
		skip := false
		for i := 1; i <= 5; i++ {
			if p.Name == localProjects[i].Name {
				skip = true
			}
		}
		if skip {
			continue
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}
	// Check that UpdateUniverse() with gc=false does not delete the local copy
	// of the project.
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if err := dirExists(localProjects[i].Path); err != nil {
			t.Fatalf("expected project %q at path %q to exist but it did not", localProjects[i].Name, localProjects[i].Path)
		}
		checkReadme(t, fake.X, localProjects[i], "initial readme")
	}
	// Check that UpdateUniverse() with gc=true does delete the local copy of
	// the project.
	if err := fake.UpdateUniverse(true); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		err := dirExists(localProjects[i].Path)
		if testDirtyProjectDelete && i >= 2 && i <= 4 {
			if err != nil {
				t.Fatalf("expected project %q at path %q to exist but it did not", localProjects[i].Name, localProjects[i].Path)
			}
		} else if err == nil {
			t.Fatalf("expected project %q at path %q not to exist but it did", localProjects[i].Name, localProjects[i].Path)
		}
	}
}
func TestUpdateUniverseDeletedProject(t *testing.T) {
	testUpdateUniverseDeletedProject(t, false)
	testUpdateUniverseDeletedProject(t, true)
}

func TestIgnoredProjectsNotDeleted(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Delete project 1.
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Name == localProjects[1].Name {
			continue
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}
	lc := project.LocalConfig{Ignore: true}
	project.WriteLocalConfig(fake.X, localProjects[1], lc)
	if err := fake.UpdateUniverse(true); err != nil {
		t.Fatal(err)
	}
	if err := dirExists(localProjects[1].Path); err != nil {
		t.Fatalf("expected project %q at path %q to exist but it did not: %s", localProjects[1].Name, localProjects[1].Path, err)
	}
}

// TestUpdateUniverseNewProjectSamePath checks that UpdateUniverse can handle a
// new project with the same path as a deleted project, but a different path.
func TestUpdateUniverseNewProjectSamePath(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Delete a project 1 and create a new one with a different name but the
	// same path.
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	newProjectName := "new-project-name"
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Path == localProjects[1].Path {
			p.Name = newProjectName
		}
		projects = append(projects, p)
	}
	localProjects[1].Name = newProjectName
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}
	// Check that UpdateUniverse() does not fail.
	if err := fake.UpdateUniverse(true); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateUniverseRemoteBranch checks that UpdateUniverse can pull from a
// non-master remote branch.
func TestUpdateUniverseRemoteBranch(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	// Commit to master branch of a project 1.
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")
	// Create and checkout a new branch of project 1 and make a new commit.
	git := gitutil.New(fake.X, gitutil.RootDirOpt(fake.Projects[localProjects[1].Name]))
	if err := git.CreateAndCheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "non-master commit")
	// Point the manifest to the new non-master branch.
	m, err := fake.ReadRemoteManifest()
	if err != nil {
		t.Fatal(err)
	}
	projects := []project.Project{}
	for _, p := range m.Projects {
		if p.Name == localProjects[1].Name {
			p.RemoteBranch = "non-master"
		}
		projects = append(projects, p)
	}
	m.Projects = projects
	if err := fake.WriteRemoteManifest(m); err != nil {
		t.Fatal(err)
	}
	// Check that UpdateUniverse pulls the commit from the non-master branch.
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}
	checkReadme(t, fake.X, localProjects[1], "non-master commit")
}

// TestUpdateWhenRemoteChangesRebased checks that UpdateUniverse can pull from a
// non-master remote branch if the local changes were rebased somewhere else(gerrit)
// before being pushed to remote
func TestUpdateWhenRemoteChangesRebased(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gitRemote := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(fake.Projects[localProjects[1].Name]))
	gr := git.NewGit(fake.Projects[localProjects[1].Name])
	if err := gitRemote.CreateAndCheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}

	gitLocal := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(localProjects[1].Path))
	gl := git.NewGit(localProjects[1].Path)
	if err := gl.Fetch("origin", git.PruneOpt(true)); err != nil {
		t.Fatal(err)
	}

	// checkout branch in local repo
	if err := gitLocal.CheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}

	// Create commits in remote repo
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "non-master commit")
	writeFile(t, fake.X, fake.Projects[localProjects[1].Name], "file1", "file1")
	file1CommitRev, _ := gr.CurrentRevision()
	writeFile(t, fake.X, fake.Projects[localProjects[1].Name], "file2", "file2")
	file2CommitRev, _ := gr.CurrentRevision()

	if err := gl.Fetch("origin", git.PruneOpt(true)); err != nil {
		t.Fatal(err)
	}

	// Cherry pick creation of file1, so that it acts like been rebased on remote repo
	// As there is a commit creating README on remote not in local repo
	if err := gitLocal.CherryPick(file1CommitRev); err != nil {
		t.Fatal(err)
	}

	if err := project.UpdateUniverse(fake.X, false, false, true /*rebaseTracked*/, false, false, project.DefaultHookTimeout); err != nil {
		t.Fatal(err)
	}

	// It rebased properly and pulled latest changes
	localRev, _ := gl.CurrentRevision()
	if file2CommitRev != localRev {
		t.Fatalf("Current commit is %v, it should be %v\n", localRev, file2CommitRev)
	}
}

func TestUpdateWhenConflictMerge(t *testing.T) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gitRemote := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(fake.Projects[localProjects[1].Name]))
	gr := git.NewGit(fake.Projects[localProjects[1].Name])
	if err := gitRemote.CreateAndCheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}

	gitLocal := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(localProjects[1].Path))
	gl := git.NewGit(localProjects[1].Path)
	if err := gl.Fetch("origin", git.PruneOpt(true)); err != nil {
		t.Fatal(err)
	}

	// checkout branch in local repo
	if err := gitLocal.CheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}

	// Create commits in remote repo
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "non-master commit")
	writeFile(t, fake.X, fake.Projects[localProjects[1].Name], "file1", "file1")
	file1CommitRev, _ := gr.CurrentRevision()
	writeFile(t, fake.X, fake.Projects[localProjects[1].Name], "file2", "file2")

	if err := gl.Fetch("origin", git.PruneOpt(true)); err != nil {
		t.Fatal(err)
	}

	// Cherry pick creation of file1, so that it acts like been rebased on remote repo
	// This would act like conflicting merge
	if err := gitLocal.CherryPick(file1CommitRev); err != nil {
		t.Fatal(err)
	}
	rev, _ := gl.CurrentRevision()

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	localRev, _ := gl.CurrentRevision()
	if rev != localRev {
		t.Fatalf("Current commit is %v, it should be %v\n", localRev, rev)
	}
	checkJiriRevFiles(t, fake.X, localProjects[1])
}

// TestCheckoutSnapshotUrl tests checking out snapshot functionality from a url
func TestCheckoutSnapshotUrl(t *testing.T) {
	testCheckoutSnapshot(t, true)
}

// TestCheckoutSnapshotFileSystem tests checking out snapshot functionality from filesystem
func TestCheckoutSnapshotFileSystem(t *testing.T) {
	testCheckoutSnapshot(t, false)
}

func testCheckoutSnapshot(t *testing.T, testURL bool) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	var oldCommitRevs []string
	var latestCommitRevs []string

	for i, localProject := range localProjects {
		gr := git.NewGit(fake.Projects[localProject.Name])
		writeFile(t, fake.X, fake.Projects[localProject.Name], "file1"+strconv.Itoa(i), "file1"+strconv.Itoa(i))
		file1CommitRev, _ := gr.CurrentRevision()
		oldCommitRevs = append(oldCommitRevs, file1CommitRev)
		writeFile(t, fake.X, fake.Projects[localProject.Name], "file2"+strconv.Itoa(i), "file2"+strconv.Itoa(i))
		file2CommitRev, _ := gr.CurrentRevision()
		latestCommitRevs = append(latestCommitRevs, file2CommitRev)
	}

	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	for i, localProject := range localProjects {
		gitLocal := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(localProject.Path))
		gl := git.NewGit(localProject.Path)
		rev, _ := gl.CurrentRevision()
		if rev != latestCommitRevs[i] {
			t.Fatalf("Current commit for project %q is %v, it should be %v\n", localProject.Name, rev, latestCommitRevs[i])
		}

		// Test case when local repo in on a branch
		if i == 1 {
			if err := gitLocal.CheckoutBranch("master"); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir, err := ioutil.TempDir("", "snap")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	manifest := &project.Manifest{}
	for _, localProject := range localProjects {
		manifest.Projects = append(manifest.Projects, localProject)
	}
	manifest.Projects[0].Revision = oldCommitRevs[0]
	manifest.Projects[1].Revision = oldCommitRevs[1]

	// Test case when snapshot specifies latest revision
	manifest.Projects[2].Revision = latestCommitRevs[2]

	manifest.Projects[3].Revision = oldCommitRevs[3]
	manifest.Projects[4].Revision = latestCommitRevs[4]
	manifest.Projects[5].Revision = oldCommitRevs[5]
	manifest.Projects[6].Revision = latestCommitRevs[6]
	snapshotFile := filepath.Join(dir, "snapshot")
	manifest.ToFile(fake.X, snapshotFile)
	if testURL {
		snapBytes, err := ioutil.ReadFile(snapshotFile)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "html/text")
			fmt.Fprintln(w, string(snapBytes[:]))
		}))
		defer server.Close()

		project.CheckoutSnapshot(fake.X, server.URL, false, project.DefaultHookTimeout)
	} else {
		project.CheckoutSnapshot(fake.X, snapshotFile, false, project.DefaultHookTimeout)
	}
	sort.Sort(project.ProjectsByPath(localProjects))
	for i, localProject := range localProjects {
		gl := git.NewGit(localProject.Path)
		rev, _ := gl.CurrentRevision()
		expectedRev := manifest.Projects[i].Revision
		if rev != expectedRev {
			t.Fatalf("Current commit for project %q is %v, it should be %v\n", localProject.Name, rev, expectedRev)
		}
	}
}

func testLocalBranchesAreUpdated(t *testing.T, shouldLocalBeOnABranch, rebaseAll bool) {
	localProjects, fake, cleanup := setupUniverse(t)
	defer cleanup()
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	gitRemote := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(fake.Projects[localProjects[1].Name]))
	if err := gitRemote.CreateAndCheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}

	// This will fetch non-master to local
	if err := fake.UpdateUniverse(false); err != nil {
		t.Fatal(err)
	}

	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "non-master commit")

	if err := gitRemote.CheckoutBranch("master"); err != nil {
		t.Fatal(err)
	}
	writeReadme(t, fake.X, fake.Projects[localProjects[1].Name], "master commit")

	gitLocal := gitutil.New(fake.X, gitutil.UserNameOpt("John Doe"), gitutil.UserEmailOpt("john.doe@example.com"), gitutil.RootDirOpt(localProjects[1].Path))

	// This will create a local branch non-master
	if err := gitLocal.CheckoutBranch("non-master"); err != nil {
		t.Fatal(err)
	}

	// Go back to detached HEAD
	if !shouldLocalBeOnABranch {
		if err := gitLocal.CheckoutBranch("HEAD", gitutil.DetachOpt(true)); err != nil {
			t.Fatal(err)
		}
	}

	if err := project.UpdateUniverse(fake.X, false, false, false, false, rebaseAll, project.DefaultHookTimeout); err != nil {
		t.Fatal(err)
	}

	if shouldLocalBeOnABranch && gitLocal.IsOnBranch() == false {
		t.Fatalf("local repo should be on the branch after update")
	} else if !shouldLocalBeOnABranch && gitLocal.IsOnBranch() == true {
		t.Fatalf("local repo should be on detached head after update")
	}

	projects, err := project.LocalProjects(fake.X, project.FastScan)
	if err != nil {
		t.Fatal(err)
	}

	states, err := project.GetProjectStates(fake.X, projects, false)
	if err != nil {
		t.Fatal(err)
	}

	state := states[localProjects[1].Key()]
	if shouldLocalBeOnABranch && state.CurrentBranch.Name != "non-master" {
		t.Fatalf("local repo should be on branch(non-master) it is on %q", state.CurrentBranch.Name)
	}

	if rebaseAll {
		for _, branch := range state.Branches {
			if branch.Tracking != nil {
				if branch.Revision != branch.Tracking.Revision {
					t.Fatalf("branch %q has different revision then remote branch %q", branch.Name, branch.Tracking.Name)
				}
			}
		}
	} else {
		for _, branch := range state.Branches {
			if branch.Tracking != nil {
				if branch.Name == state.CurrentBranch.Name {
					if branch.Revision != branch.Tracking.Revision {
						t.Fatalf("branch %q has different revision then remote branch %q", branch.Name, branch.Tracking.Name)
					}
				} else if branch.Revision == branch.Tracking.Revision {
					t.Fatalf("branch %q should have different revision then remote branch %q", branch.Name, branch.Tracking.Name)
				}
			}
		}
	}
}

// TestLocalBranchesAreUpdatedWhenOnHead test that all the local branches are
// updated on jiri update when local repo is on detached head
func TestLocalBranchesAreUpdatedWhenOnHead(t *testing.T) {
	testLocalBranchesAreUpdated(t, false, true)
}

// TestLocalBranchesAreUpdatedWhenOnBranch test that all the local branches are
// updated on jiri update when local repo is on a branch
func TestLocalBranchesAreUpdatedWhenOnBranch(t *testing.T) {
	testLocalBranchesAreUpdated(t, true, true)
}

// TestLocalBranchesNotUpdatedWhenOnHead test that all the local branches are not
// updated on jiri update when local repo is on detached head and rebaseAll is false
func TestLocalBranchesNotUpdatedWhenOnHead(t *testing.T) {
	testLocalBranchesAreUpdated(t, false, false)
}

// TestLocalBranchesAreUpdatedWhenOnBranch test that all the local branches are not
// updated on jiri update when local repo is on a branch and rebaseAll is false
func TestLocalBranchesNotUpdatedWhenOnBranch(t *testing.T) {
	testLocalBranchesAreUpdated(t, true, false)
}

func TestFileImportCycle(t *testing.T) {
	jirix, cleanup := jiritest.NewX(t)
	defer cleanup()

	// Set up the cycle .jiri_manifest -> A -> B -> A
	jiriManifest := project.Manifest{
		LocalImports: []project.LocalImport{
			{File: "A"},
		},
	}
	manifestA := project.Manifest{
		LocalImports: []project.LocalImport{
			{File: "B"},
		},
	}
	manifestB := project.Manifest{
		LocalImports: []project.LocalImport{
			{File: "A"},
		},
	}
	if err := jiriManifest.ToFile(jirix, jirix.JiriManifestFile()); err != nil {
		t.Fatal(err)
	}
	if err := manifestA.ToFile(jirix, filepath.Join(jirix.Root, "A")); err != nil {
		t.Fatal(err)
	}
	if err := manifestB.ToFile(jirix, filepath.Join(jirix.Root, "B")); err != nil {
		t.Fatal(err)
	}

	// The update should complain about the cycle.
	err := project.UpdateUniverse(jirix, false, false, false, false, false, project.DefaultHookTimeout)
	if got, want := fmt.Sprint(err), "import cycle detected in local manifest files"; !strings.Contains(got, want) {
		t.Errorf("got error %v, want substr %v", got, want)
	}
}

func TestRemoteImportCycle(t *testing.T) {
	fake, cleanup := jiritest.NewFakeJiriRoot(t)
	defer cleanup()

	// Set up two remote manifest projects, remote1 and remote1.
	if err := fake.CreateRemoteProject("remote1"); err != nil {
		t.Fatal(err)
	}
	if err := fake.CreateRemoteProject("remote2"); err != nil {
		t.Fatal(err)
	}
	remote1 := fake.Projects["remote1"]
	remote2 := fake.Projects["remote2"]

	fileA, fileB := filepath.Join(remote1, "A"), filepath.Join(remote2, "B")

	// Set up the cycle .jiri_manifest -> remote1+A -> remote2+B -> remote1+A
	jiriManifest := project.Manifest{
		Imports: []project.Import{
			{Manifest: "A", Name: "n1", Remote: remote1},
		},
	}
	manifestA := project.Manifest{
		Imports: []project.Import{
			{Manifest: "B", Name: "n2", Remote: remote2},
		},
	}
	manifestB := project.Manifest{
		Imports: []project.Import{
			{Manifest: "A", Name: "n3", Remote: remote1},
		},
	}
	if err := jiriManifest.ToFile(fake.X, fake.X.JiriManifestFile()); err != nil {
		t.Fatal(err)
	}
	if err := manifestA.ToFile(fake.X, fileA); err != nil {
		t.Fatal(err)
	}
	if err := manifestB.ToFile(fake.X, fileB); err != nil {
		t.Fatal(err)
	}
	commitFile(t, fake.X, remote1, fileA, "commit A")
	commitFile(t, fake.X, remote2, fileB, "commit B")

	// The update should complain about the cycle.
	err := project.UpdateUniverse(fake.X, false, false, false, false, false, project.DefaultHookTimeout)
	if got, want := fmt.Sprint(err), "import cycle detected in remote manifest imports"; !strings.Contains(got, want) {
		t.Errorf("got error %v, want substr %v", got, want)
	}
}

func TestFileAndRemoteImportCycle(t *testing.T) {
	fake, cleanup := jiritest.NewFakeJiriRoot(t)
	defer cleanup()

	// Set up two remote manifest projects, remote1 and remote2.
	// Set up two remote manifest projects, remote1 and remote1.
	if err := fake.CreateRemoteProject("remote1"); err != nil {
		t.Fatal(err)
	}
	if err := fake.CreateRemoteProject("remote2"); err != nil {
		t.Fatal(err)
	}
	remote1 := fake.Projects["remote1"]
	remote2 := fake.Projects["remote2"]
	fileA, fileD := filepath.Join(remote1, "A"), filepath.Join(remote1, "D")
	fileB, fileC := filepath.Join(remote2, "B"), filepath.Join(remote2, "C")

	// Set up the cycle .jiri_manifest -> remote1+A -> remote2+B -> C -> remote1+D -> A
	jiriManifest := project.Manifest{
		Imports: []project.Import{
			{Manifest: "A", Root: "r1", Name: "n1", Remote: remote1},
		},
	}
	manifestA := project.Manifest{
		Imports: []project.Import{
			{Manifest: "B", Root: "r2", Name: "n2", Remote: remote2},
		},
	}
	manifestB := project.Manifest{
		LocalImports: []project.LocalImport{
			{File: "C"},
		},
	}
	manifestC := project.Manifest{
		Imports: []project.Import{
			{Manifest: "D", Root: "r3", Name: "n3", Remote: remote1},
		},
	}
	manifestD := project.Manifest{
		LocalImports: []project.LocalImport{
			{File: "A"},
		},
	}
	if err := jiriManifest.ToFile(fake.X, fake.X.JiriManifestFile()); err != nil {
		t.Fatal(err)
	}
	if err := manifestA.ToFile(fake.X, fileA); err != nil {
		t.Fatal(err)
	}
	if err := manifestB.ToFile(fake.X, fileB); err != nil {
		t.Fatal(err)
	}
	if err := manifestC.ToFile(fake.X, fileC); err != nil {
		t.Fatal(err)
	}
	if err := manifestD.ToFile(fake.X, fileD); err != nil {
		t.Fatal(err)
	}
	commitFile(t, fake.X, remote1, fileA, "commit A")
	commitFile(t, fake.X, remote2, fileB, "commit B")
	commitFile(t, fake.X, remote2, fileC, "commit C")
	commitFile(t, fake.X, remote1, fileD, "commit D")

	// The update should complain about the cycle.
	err := project.UpdateUniverse(fake.X, false, false, false, false, false, project.DefaultHookTimeout)
	if got, want := fmt.Sprint(err), "import cycle detected"; !strings.Contains(got, want) {
		t.Errorf("got error %v, want substr %v", got, want)
	}
}

func TestManifestToFromBytes(t *testing.T) {
	tests := []struct {
		Manifest project.Manifest
		XML      string
	}{
		{
			project.Manifest{},
			`<manifest>
</manifest>
`,
		},
		{
			project.Manifest{
				Imports: []project.Import{
					{
						Manifest:     "manifest1",
						Name:         "remoteimport1",
						Remote:       "remote1",
						RemoteBranch: "master",
					},
					{
						Manifest:     "manifest2",
						Name:         "remoteimport2",
						Remote:       "remote2",
						RemoteBranch: "branch2",
					},
				},
				LocalImports: []project.LocalImport{
					{File: "fileimport"},
				},
				Projects: []project.Project{
					{
						Name:         "project1",
						Path:         "path1",
						Remote:       "remote1",
						RemoteBranch: "master",
						Revision:     "HEAD",
						GerritHost:   "https://test-review.googlesource.com",
						GitHooks:     "path/to/githooks",
					},
					{
						Name:         "project2",
						Path:         "path2",
						Remote:       "remote2",
						RemoteBranch: "branch2",
						Revision:     "rev2",
					},
				},
				Hooks: []project.Hook{
					{
						Name:        "testhook",
						ProjectName: "project1",
						Action:      "action.sh",
					},
				},
			},
			`<manifest>
  <imports>
    <import manifest="manifest1" name="remoteimport1" remote="remote1"/>
    <import manifest="manifest2" name="remoteimport2" remote="remote2" remotebranch="branch2"/>
    <localimport file="fileimport"/>
  </imports>
  <projects>
    <project name="project1" path="path1" remote="remote1" gerrithost="https://test-review.googlesource.com" githooks="path/to/githooks"/>
    <project name="project2" path="path2" remote="remote2" remotebranch="branch2" revision="rev2"/>
  </projects>
  <hooks>
    <hook name="testhook" action="action.sh" project="project1"/>
  </hooks>
</manifest>
`,
		},
	}
	for _, test := range tests {
		gotBytes, err := test.Manifest.ToBytes()
		if err != nil {
			t.Errorf("%+v ToBytes failed: %v", test.Manifest, err)
		}
		if got, want := string(gotBytes), test.XML; got != want {
			t.Errorf("%+v ToBytes GOT\n%v\nWANT\n%v", test.Manifest, got, want)
		}
		manifest, err := project.ManifestFromBytes([]byte(test.XML))
		if err != nil {
			t.Errorf("%+v FromBytes failed: %v", test.Manifest, err)
		}
		if got, want := manifest, &test.Manifest; !reflect.DeepEqual(got, want) {
			t.Errorf("%+v FromBytes got %#v, want %#v", test.Manifest, got, want)
		}
	}
}

func TestProjectToFromFile(t *testing.T) {
	jirix, cleanup := jiritest.NewX(t)
	defer cleanup()

	tests := []struct {
		Project project.Project
		XML     string
	}{
		{
			// Default fields are dropped when marshaled, and added when unmarshaled.
			project.Project{
				Name:         "project1",
				Path:         filepath.Join(jirix.Root, "path1"),
				Remote:       "remote1",
				RemoteBranch: "master",
				Revision:     "HEAD",
			},
			`<project name="project1" path="path1" remote="remote1"/>
`,
		},
		{
			project.Project{
				Name:         "project2",
				Path:         filepath.Join(jirix.Root, "path2"),
				GitHooks:     filepath.Join(jirix.Root, "git-hooks"),
				Remote:       "remote2",
				RemoteBranch: "branch2",
				Revision:     "rev2",
			},
			`<project name="project2" path="path2" remote="remote2" remotebranch="branch2" revision="rev2" githooks="git-hooks"/>
`,
		},
	}
	for index, test := range tests {
		filename := filepath.Join(jirix.Root, fmt.Sprintf("test-%d", index))
		if err := test.Project.ToFile(jirix, filename); err != nil {
			t.Errorf("%+v ToFile failed: %v", test.Project, err)
		}
		gotBytes, err := ioutil.ReadFile(filename)
		if err != nil {
			t.Errorf("%+v ReadFile failed: %v", test.Project, err)
		}
		if got, want := string(gotBytes), test.XML; got != want {
			t.Errorf("%+v ToFile GOT\n%v\nWANT\n%v", test.Project, got, want)
		}
		project, err := project.ProjectFromFile(jirix, filename)
		if err != nil {
			t.Errorf("%+v FromFile failed: %v", test.Project, err)
		}
		if got, want := project, &test.Project; !reflect.DeepEqual(got, want) {
			t.Errorf("%+v FromFile got %#v, want %#v", test.Project, got, want)
		}
	}
}
