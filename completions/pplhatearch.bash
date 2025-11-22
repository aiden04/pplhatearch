#!/usr/bin/env bash
#
# pplhatearch bash completion (minimal yay-like helper)

_pplhatearch()
{
    local cur prev opts value
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    opts="-S -R -Ss -Si -Q -C -repo -color -n -s -d -f -h --help"

    case "${prev}" in
        -C)
            compopt -o dirnames 2>/dev/null
            COMPREPLY=( $(compgen -d -- "${cur}") )
            return 0
            ;;
        -repo)
            COMPREPLY=( $(compgen -W "https://github.com/archlinux/aur.git" -- "${cur}") )
            return 0
            ;;
    esac

    if [[ "${cur}" == -* ]]; then
        COMPREPLY=( $(compgen -W "${opts}" -- "${cur}") )
        return 0
    fi

    # fallback to filesystem completion for package arguments
    COMPREPLY=( $(compgen -f -- "${cur}") )
    return 0
}

complete -F _pplhatearch pplhatearch
