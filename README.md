# Upgrade

Tool for upgrading a go module's major version, or the major version of one of
its dependencies.

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
