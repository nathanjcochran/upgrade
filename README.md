# Upgrade

Tool for upgrading a go dependency's major version.

## Usage

```
Usage: upgrade [-d directory] [-v] [module [version]]

Upgrades the named module dependency to the specified version, or, if no
version is given, to the highest major/minor/patch version available. Updates
the dependency's require directive in the go.mod file, as well as the relevant
import paths in all Go files in the current module (the current package and
all subpackages).

The module should be given as a fully qualified module path, as it is written
in the go.mod file (including the major version component, if applicable).

For example: github.com/nathanjcochran/upgrade

The version must be a valid semver module version. It can be provided with any
level of major/minor/patch specificity - e.g. 'v2', 'v2.3', 'v.2.3.4'. The
tool will always attempt to upgrade to the highest available matching version.

If no module is given, or if the special target "all" is given, the tool
will attempt to upgrade all direct dependencies in the go.mod file to the
highest major version available.

Note that updating major versions of dependencies is likely to introduce
compilation and/or runtime errors due to backwards-incompatibile changes. You
should be prepared to address errors after the upgrade process completes.

By default, the tool assumes the module to update is rooted in the current
directory. The [-d directory] flag can be provided to override that behavior.

The [-v] flag turns on verbose output.

Options:
  -d string
    	Module directory path (default ".")
  -v	verbose output
```
