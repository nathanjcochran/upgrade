# Upgrade

Tool for upgrading a go dependency's major version.

## Usage

```
Usage: upgrade [-f modfile path] [-v] module [version]:

Upgrades the named module dependency to the specified version,
or, if no version is given, to the highest major version available.

The module should be given as a fully qualified module path
(including the major version component, if applicable).
For example: github.com/nathanjcochran/gomod.

  -f string
    	go.mod file path (default "./go.mod")
  -v	verbose output
```
