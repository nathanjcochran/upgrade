# Upgrade

Tool for upgrading a go module's major version, or the major version of one of
its dependencies.

This tool's only external dependency is the `go list` command (it does not
directly call out to any version control systems).

<!-- doctoc README.md --github --title '## Table of Contents' -->
<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
## Table of Contents

- [Usage](#usage)
- [Examples](#examples)
  - [Upgrading the Current Module](#upgrading-the-current-module)
    - [Incrementing the Major Version](#incrementing-the-major-version)
    - [Changing to a Specific Major Version](#changing-to-a-specific-major-version)
    - [Downgrading the Major Version](#downgrading-the-major-version)
  - [Upgrading Dependencies](#upgrading-dependencies)
    - [All Dependencies](#all-dependencies)
    - [Highest Available Major Version](#highest-available-major-version)
    - [Specific Dependency Version](#specific-dependency-version)
    - [Downgrading a Dependency](#downgrading-a-dependency)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->


## Usage

```
upgrade [-d dir] [-v] [module] [version]

Options:
  -d string
    	Module directory path (default ".")
  -v	verbose output
```

Upgrades the major version of a module, or the major version of one of its
dependencies, by editing the module's go.mod file and the corresponding import
statements in its .go files.

If no arguments are given, upgrades the major version of the module rooted in
the current working directory by incrementing the major version component of
its module path (or adding the version component, if necessary).

The same behavior is triggered by supplying the module's own path for the
[module] argument. However, in that form, a target [version] can also be given,
making it possible to jump several major versions at once, or to downgrade
versions.

If the module path of a dependency is given, upgrades the dependency to the
specified version, or, if no version is given, to the highest major version
available.

If the special target "all" is given, attempts to upgrade all direct
dependencies in the go.mod file to the highest major version available.

If given, [module] must be a fully qualified module path, as written in the
go.mod file. It must include the major version component, if applicable. For
example: "github.com/nathanjcochran/upgrade/v2".

If [version] is given, it must be a valid semver module version. It can be
provided with any level of major/minor/patch specificity - e.g. 'v2', 'v2.3',
'v.2.3.4'. When upgrading the current module, only the major component of the
provided version is taken into account (the minor/patch versions are ignored).
When upgrading a dependency, the tool will attempt to upgrade to the highest
available matching version, unless the target major version of the dependency
is already required, in which case it will maintain the existing minor/patch
version.

NOTE: This tool does not add version tags in any version control systems.

By default, the tool assumes the module being updated is rooted in the current
directory. The [-d directory] flag can be provided to override that behavior.

The [-v] flag turns on verbose output.

## Examples

### Upgrading the Current Module

#### Incrementing the Major Version

To upgrade the major version of the module in the current working directly to
the next logical major version, simply run the `upgrade` without any arguments.

For example, to upgrade `github.com/nathanjcochran/upgrade/v2` to major
version `v3` (the next logical major version), run:

```
upgrade
```

This is equivalent to, and is basically shorthand for, providing the module's
own module path for the [module] argument:

```
upgrade github.com/nathanjcochran/upgrade/v2
```

Note that this would also work if the module didn't yet have the major version
component in its important path, in which case it would upgrade the module to
major version `v2` (for example, `github.com/nathanjcochran/upgrade` to
`github.com/nathanjcochran/upgrade/v2`).

#### Changing to a Specific Major Version

To change the major version of the module in the current working directory to a
specific major version (for example, to skip immediately to a higher major
version), give the module's own path for the [module] argument and the target
version for the [version] argument:

For example, to change the major version of `github.com/nathanjcochran/upgrade`
to major version `v3` (skipping over `v2`), run:

```
upgrade github.com/nathanjcochran/upgrade v3
```

#### Downgrading the Major Version

Downgrading the major version of a module is the same as upgrading to a specific
major version. For example, to downgrade `github.com/nathanjcochran/upgrade/v3`
to major version `v2`, run:

```
upgrade github.com/nathanjcochran/upgrade/v3 v2
```

### Upgrading Dependencies

#### All Dependencies

To upgrade all direct dependencies to the highest available major version, give
the special "all" target for the [module] argument:

```
upgrade all
```

Note that this command can take awhile. This slowness is almost entirely due to
external calls made to `go list` to find the highest available major version for
each dependency.

#### Highest Available Major Version

To upgrade the major version of a dependency to the highest available major
version, provide the module path of the dependency for the [module] argument.
For example, to upgrade `github.com/some/dependency/v2`to the highest available
major version, run:

```
upgrade github.com/some/dependency/v2
```

#### Specific Dependency Version

To upgrade a dependency to a specific target version, rather than the highest
available major version, provide the [version] argument. For example, to upgrade
the `github.com/some/dependency/v2` dependency to `v4` (even if, for example,
`v5` was available), run:

```
upgrade github.com/some/dependency/v2 v4
```

This will upgrade `github.com/some/dependency` to the highest available
minor/patch version available for major version `v4` (i.e. the highest version in
the range `v4.x.x`).

You can also give a more specific target version, so long as it is a valid
semver version number. For example, to upgrade
`github.com/nathanjcochran/upgrade/v2` to the highest available patch version
for minor version `v4.2` (i.e. the highest version in the range `v4.2.x`, even
if, for example, `v4.3` was available), run:

```
upgrade github.com/nathanjcochran/upgrade/v2 v4.2
```

To update to a specific patch version, for example `v.4.2.9`, run:

```
upgrade github.com/nathanjcochran/upgrade/v2 v4.2.9
```

#### Downgrading a Dependency

Downgrading the major version of a dependency is the same as upgrading it to a
specific major version. For example, to downgrade
`github.com/some/dependency/v3` to the highest available patch version in the
range `v2.5.x`, run:

```
upgrade github.com/some/dependency/v3 v2.5
```
