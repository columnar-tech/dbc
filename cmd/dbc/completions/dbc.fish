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
complete -c dbc -n '__fish_dbc_needs_command' -l quiet -s q -d 'Suppress all output'

# Subcommands
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'install' -d 'Install a driver'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'uninstall' -d 'Uninstall a driver'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'init' -d 'Create new driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'add' -d 'Add a driver to the driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'sync' -d 'Install all drivers in the driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'search' -d 'Search for drivers'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'remove' -d 'Remove a driver from the driver list'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'info' -d 'Get detailed information about a specific driver'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'docs' -d 'Open driver documentation in a web browser'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'completion' -d 'Generate shell completions'
complete -f -c dbc -n '__fish_dbc_needs_command' -a 'auth' -d 'Authenticate with a driver registry'

# install subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -s h -d 'Show Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l help -d 'Show Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l json -d 'Print output as JSON instead of plaintext'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l no-verify -d 'Do not verify the driver after installation'
complete -f -c dbc -n '__fish_dbc_using_subcommand install' -l level -s l -d 'Installation level' -xa 'user system'

# uninstall subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand uninstall' -l json -d 'Print output as JSON instead of plaintext'
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
complete -f -c dbc -n '__fish_dbc_using_subcommand search' -l json -d 'Print output as JSON instead of plaintext'

# remove subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand remove' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand remove' -l help -d 'Help'
complete -c dbc -n '__fish_dbc_using_subcommand remove' -l path -s p -r -F -a '*.toml' -d 'Driver list to remove from'

# info subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand info' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand info' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand info' -l json -d 'Print output as JSON instead of plaintext'

# docs subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand docs' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand docs' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand docs' -l no-open -d 'Print the documentation URL instead of opening it in a web browser'

# completion subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -a 'bash' -d 'Generate autocompletion script for bash'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -a 'zsh' -d 'Generate autocompletion script for zsh'
complete -f -c dbc -n '__fish_dbc_using_subcommand completion' -a 'fish' -d 'Generate autocompletion script for fish'

# Helper function to check if we're using auth subcommand and need a nested subcommand
function __fish_dbc_auth_needs_subcommand
    set -l cmd (commandline -opc)
    if test (count $cmd) -eq 2
        if test $cmd[2] = "auth"
            return 0
        end
    end
    return 1
end

# Helper function to check if we're using a specific auth subcommand
function __fish_dbc_auth_using_subcommand
    set -l cmd (commandline -opc)
    if test (count $cmd) -gt 2
        if test $cmd[2] = "auth" -a $argv[1] = $cmd[3]
            return 0
        end
    end
    return 1
end

# auth subcommand
complete -f -c dbc -n '__fish_dbc_using_subcommand auth' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_using_subcommand auth' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_auth_needs_subcommand' -a 'login' -d 'Authenticate with a driver registry'
complete -f -c dbc -n '__fish_dbc_auth_needs_subcommand' -a 'logout' -d 'Log out from a driver registry'

# auth login subcommand
complete -f -c dbc -n '__fish_dbc_auth_using_subcommand login' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_auth_using_subcommand login' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_auth_using_subcommand login' -l api-key -d 'Authenticate using an API key instead of OAuth'

# auth logout subcommand
complete -f -c dbc -n '__fish_dbc_auth_using_subcommand logout' -s h -d 'Help'
complete -f -c dbc -n '__fish_dbc_auth_using_subcommand logout' -l help -d 'Help'
complete -f -c dbc -n '__fish_dbc_auth_using_subcommand logout' -l purge -d 'Remove all local auth credentials for dbc'
