# JIRI

[TOC]

## How Do I

### rebase current branch instead of fast-forwarding it

`jiri update -rebase-current`

### rebase all my branches

Run  `jiri update -rebase-all`. This will not rebase un-tracked branches.

### rebase my untracked branches

Run `jiri update -rebase-untracked` to rebase your current un-tracked branch. To rebase all un-tracked branches use `jiri update -rebase-all -rebase-untracked`.

### test my local manifest changes

`jiri update -local-manifest`

### stop jiri from updating my project

Use `jiri project-config`. [See this](/behaviour.md#intended-project-config) for it's intended behavior.
Current config can be displayed using command `jiri project-config`.
To change a config use
```
jiri project-config [-flag=true|false]
```
where flags are `-ignore`, `no-rebase`, `no-update`

### check if all my projects are on `JIRI_HEAD` {#use-jiri-status}

Run `jiri status ` for that. This command returns all projects which are not on `JIRI_HEAD`, or have un-merged commits, or have un-committed changes.

To just get projects which are **not** on **JIRI_HEAD** run
```
jiri status -changes=false -commits=false
```
### run a command inside all my projects

`jiri runp [command]`

### grep across projects

`jiri grep [text]`

### delete branch across projects

Run `jiri branch -d [branch_name]`, this will run `git branch -d [branch_name]` in all the projects. `-D` can also be used to replicate functionality of `git branch -D`.

### get projects and branches other than master

`jiri branch`

### download whole gerrit topic

`jiri patch -topic <topic>`

### update jiri without updating projects

`jiri selfupdate`

### use upload to push CL

[See This](/README.md#Gerrit-CL-workflow)

### get JIRI_HEAD revision of a project

`git rev-parse JIRI_HEAD` from inside the project.

### get current revision of a project

`jiri project [project-name]`

### clean project(s)

Run `jiri project [-clean|clean-all] [project-name]`. See it's [intended behaviour](/behaviour.md#intended-project-clean).

### get help

Run `jiri help` to see all the commands and `jiri help [command]` to get help for that command.

To provide feedback [see this](/behaviour.md#feedback).
