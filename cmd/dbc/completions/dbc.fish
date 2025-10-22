# fish completion for dbc            -*- shell-script -*-

# Helper function to check if a subcommand has been given
function __fish_dbc_needs_command
    set -l cmd (commandline -opc)
    set -e cmd[1]
    if test (count $cmd) -eq 0
        return 0
    end
    return 1
end

# Helper function to check if we're using a specific subcommand
function __fish_dbc_using_subcommand
    set -l cmd (commandline -opc)
    if test (count $cmd) -gt 1
        if test $argv[1] = $cmd[2]
            return 0
        end
    end
    return 1
end

# Global options
complete -c dbc -n '__fish_dbc_needs_command' -l help -d 'Show help'
complete -c dbc -n '__fish_dbc_needs_command' -s h -d 'Show help'
complete -c dbc -n '__fish_dbc_needs_command' -l version -d 'Show version'

# Subcommands
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'install' -d 'Install a driver'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'uninstall' -d 'Uninstall a driver'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'init' -d 'Create new driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'add' -d 'Add a driver to the driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'sync' -d 'Install all drivers in the driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'search' -d 'Search for drivers'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'remove' -d 'Remove a driver from the driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'info' -d 'Get detailed information about a specific driver'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'completion' -d 'Generate shell completions'

# install subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -s h -d 'Show Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l help -d 'Show Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l no-verify -d 'Do not verify the driver after installation'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l level -s l -d 'Installation level' -xa 'user system'

# uninstall subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand uninstall' -l level -s l -d 'Installation level' -xa 'user system'

# init subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand init' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand init' -l help -d 'Help'
complete -c dbc -n '__fish_dbc_using_subcommand init' -F -a '*.toml' -d 'File to create'

# add subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand add' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand add' -l help -d 'Help'
complete -c dbc -n '__fish_dbc_using_subcommand add' -l path -s p -r -F -a '*.toml' -d 'Driver list to add to'

# sync subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand sync' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand sync' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand sync' -l level -s l -d 'Installation level' -xa 'user system'
complete -c dbc -n '__fish_dbc_using_subcommand sync' -l path -s p -r -F -a '*.toml' -d 'Driver list to sync'
complete -f -c dbc -n '__fish_dbc_using_subcommand sync' -l no-verify -d 'Do not verify the driver after installation'

# search subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand search' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand search' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand search' -s v -d 'Verbose'

# remove subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand remove' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand remove' -l help -d 'Help'
complete -c dbc -n '__fish_dbc_using_subcommand remove' -l path -s p -r -F -a '*.toml' -d 'Driver list to remove from'

# completion subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -a 'bash' -d 'Generate autocompletion script for bash'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -a 'zsh' -d 'Generate autocompletion script for zsh'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -a 'fish' -d 'Generate autocompletion script for fish'
