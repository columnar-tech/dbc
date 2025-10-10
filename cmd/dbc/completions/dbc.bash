# bash completion for dbc                 -*- shell-script -*-

_dbc() {
    local cur prev words cword
    _init_completion || return

    local subcommands="install uninstall init add sync search remove"
    local global_opts="--help -h --version"

    # If we're completing the first argument (subcommand)
    if [[ $cword -eq 1 ]]; then
        COMPREPLY=($(compgen -W "$subcommands $global_opts" -- "$cur"))
        return 0
    fi

    # Get the subcommand (first argument)
    local subcommand="${words[1]}"

    case "$subcommand" in
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
        *)
            COMPREPLY=()
            ;;
    esac
}

_dbc_install_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --level|-l)
            COMPREPLY=($(compgen -W "user system" -- "$cur"))
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "--no-verify --level -l" -- "$cur"))
        return 0
    fi

    # Driver name completion (no specific completion available)
    COMPREPLY=()
}

_dbc_uninstall_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --level|-l)
            COMPREPLY=($(compgen -W "user system" -- "$cur"))
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "--level -l" -- "$cur"))
        return 0
    fi

    # Driver name completion (no specific completion available)
    COMPREPLY=()
}

_dbc_init_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-h" -- "$cur"))
        return 0
    fi

    # Complete .toml files
    COMPREPLY=($(compgen -f -X '!*.toml' -- "$cur"))
    # Add directory completion as well
    if [[ -d "$cur" ]]; then
        COMPREPLY+=($(compgen -d -- "$cur"))
    fi
}

_dbc_add_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --path|-p)
            # Complete .toml files
            COMPREPLY=($(compgen -f -X '!*.toml' -- "$cur"))
            if [[ -d "$cur" ]]; then
                COMPREPLY+=($(compgen -d -- "$cur"))
            fi
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-h --path -p" -- "$cur"))
        return 0
    fi

    # Driver name completion (no specific completion available)
    COMPREPLY=()
}

_dbc_sync_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --level|-l)
            COMPREPLY=($(compgen -W "user system" -- "$cur"))
            return 0
            ;;
        --path|-p)
            # Complete .toml files
            COMPREPLY=($(compgen -f -X '!*.toml' -- "$cur"))
            if [[ -d "$cur" ]]; then
                COMPREPLY+=($(compgen -d -- "$cur"))
            fi
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-h --level -l --path -p --no-verify" -- "$cur"))
        return 0
    fi

    COMPREPLY=()
}

_dbc_search_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-h -v -n" -- "$cur"))
        return 0
    fi

    # Search term completion (no specific completion available)
    COMPREPLY=()
}

_dbc_remove_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --path|-p)
            # Complete .toml files
            COMPREPLY=($(compgen -f -X '!*.toml' -- "$cur"))
            if [[ -d "$cur" ]]; then
                COMPREPLY+=($(compgen -d -- "$cur"))
            fi
            return 0
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-h --path -p" -- "$cur"))
        return 0
    fi

    # Driver name completion (no specific completion available)
    COMPREPLY=()
}

# Register the completion function
complete -F _dbc dbc

# ex: ts=4 sw=4 et filetype=sh
