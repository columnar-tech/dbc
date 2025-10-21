#compdef dbc
compdef _dbc dbc

# zsh completion for dbc                -*- shell-script -*-

function _dbc {
    local line state

    _arguments -C \
        "1: :->subcommand" \
        "*::arg:->args"
    
    case $state in
        subcommand)
            _values "dbc command" \
                'install[Install a driver]' \
                'uninstall[Uninstall a driver]' \
                'init[Create new driver list]' \
                'add[Add a driver to the driver list]' \
                'sync[Install all drivers in the driver list]' \
                'search[Search for drivers]' \
                'remove[Remove a driver from the driver list]' \
                'completion[Generate shell completions]' \
                '--help[Show help]' \
                '-h[Show help]' \
                '--version[Show version]'
        ;;
        args)
            case $line[1] in
                install)
                    _dbc_install_completions
                ;;
                uninstall)
                    _dbc_uninstall_completions
                ;;
                init)
                    _dbc_init_completions
                ;;
                add)
                    _dbc_add_completions
                ;;
                sync)
                    _dbc_sync_completions
                ;;
                search)
                    _dbc_search_completions
                ;;
                remove)
                    _dbc_remove_completions
                ;;
                completion)
                    _dbc_completion_completions
                ;;
            esac
        ;;
    esac
}

function _dbc_install_completions {    
    _arguments \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '--no-verify[do not verify the driver after installation]' \
        '(-l)--level[installation level]: :(user system)' \
        '(--level)-l[installation level]: :(user system)' \
        ':driver name: '
}

function _dbc_uninstall_completions {
    _arguments \
        '(-l)--level[installation level]: :(user system)' \
        '(--level)-l[installation level]: :(user system)' \
        ':driver name: '
}

function _dbc_init_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        ':file to create:_files -g \*.toml'
}

function _dbc_add_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '(-p)--path[driver list to add to]: :_files -g \*.toml' \
        '(--path)-p[driver list to add to]: :_files -g \*.toml' \
        ':driver name: '        
}

function _dbc_sync_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '(-l)--level[installation level]: :(user system)' \
        '(--level)-l[installation level]: :(user system)' \
        '(-p)--path[driver list to add to]: :_files -g \*.toml' \
        '(--path)-p[driver list to add to]: :_files -g \*.toml' \
        '--no-verify[do not verify the driver after installation]'
}

function _dbc_search_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '-v[verbose]' \
        '-n[names only]' \
        ':search term: '
}

function _dbc_remove_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '(-p)--path[driver list to remove from]: :_files -g \*.toml' \
        '(--path)-p[driver list to remove from]: :_files -g \*.toml' \
        ':driver name: '
}

function _dbc_completion_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        ':shell type:(bash zsh fish)'
}

# don't run the completion function when being source-d or eval-d
if [ "$funcstack[1]" = "_dbc" ]; then
    _dbc
fi
