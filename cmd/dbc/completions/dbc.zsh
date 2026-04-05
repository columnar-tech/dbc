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
                'info[Get detailed information about a specific driver]' \
                'docs[Open driver documentation in a web browser]' \
                'remove[Remove a driver from the driver list]' \
                'completion[Generate shell completions]' \
                'auth[Authenticate with a driver registry]' \
                '--help[Show help]' \
                '-h[Show help]' \
                '--version[Show version]' \
                '--quiet[Suppress all output]' \
                '-q[Suppress all output]'
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
                info)
                    _dbc_info_completions
                ;;
                docs)
                    _dbc_docs_completions
                ;;
                remove)
                    _dbc_remove_completions
                ;;
                completion)
                    _dbc_completion_completions
                ;;
                auth)
                    _dbc_auth_completions
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
        '--json[Print output as JSON instead of plaintext]' \
        '--pre[Allow implicit installation of pre-release versions]' \
        '(-l)--level[installation level]: :(user system)' \
        '(--level)-l[installation level]: :(user system)' \
        ':driver name: '
}

function _dbc_uninstall_completions {
    _arguments \
        '(-l)--level[installation level]: :(user system)' \
        '(--level)-l[installation level]: :(user system)' \
        '--json[Print output as JSON instead of plaintext]' \
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
        '--pre[Allow pre-release versions implicitly]' \
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
        '--json[Print output as JSON instead of plaintext]' \
        '--pre[Include pre-release drivers and versions (hidden by default)]' \
        ':search term: '
}

function _dbc_info_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '--json[Print output as JSON instead of plaintext]' \
        ':driver name: '
}

function _dbc_docs_completions {
    _arguments  \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '--no-open[Print the documentation URL instead of opening it in a web browser]' \
        ':driver name: '
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

function _dbc_auth_completions {
    local line state

    _arguments -C \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        "1: :->auth_subcommand" \
        "*::arg:->auth_args"

    case $state in
        auth_subcommand)
            _values "auth subcommand" \
                'login[Authenticate with a driver registry]' \
                'logout[Log out from a driver registry]'
        ;;
        auth_args)
            case $line[1] in
                login)
                    _dbc_auth_login_completions
                ;;
                logout)
                    _dbc_auth_logout_completions
                ;;
            esac
        ;;
    esac
}

function _dbc_auth_login_completions {
    _arguments \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '--api-key[Authenticate using an API key instead of OAuth]: :' \
        ':registry URL: '
}

function _dbc_auth_logout_completions {
    _arguments \
        '(--help)-h[Help]' \
        '(-h)--help[Help]' \
        '--purge[Remove all local auth credentials for dbc]' \
        ':registry URL: '
}

# don't run the completion function when being source-d or eval-d
if [ "$funcstack[1]" = "_dbc" ]; then
    _dbc
fi
