# pplhatearch fish completion

set -l __pplhatearch_opts_common "-S" "-R" "-color" "-n" "-s" "-d" "-f"

complete -c pplhatearch -n "not __fish_seen_subcommand_from -S -R" -s S -d "Install (clone/build) packages"
complete -c pplhatearch -n "not __fish_seen_subcommand_from -S -R" -s R -d "Remove packages"
complete -c pplhatearch -o Ss -d "Search the backup AUR mirror" -r
complete -c pplhatearch -o Si -d "Show package information" -r
complete -c pplhatearch -s Q -d "List packages installed via pplhatearch"
complete -c pplhatearch -l clear-cache -d "Remove cached package data and exit"
complete -c pplhatearch -s C -d "Cache directory" -r -f -a "(__fish_complete_directories)"
complete -c pplhatearch -l repo -d "Git mirror URL" -r
complete -c pplhatearch -l color -d "Enable/disable colored output"
complete -c pplhatearch -s n -d "Pass -n to pacman during removal"
complete -c pplhatearch -s s -d "Pass -s to pacman during removal"
complete -c pplhatearch -s d -d "Pass -d to pacman during removal"
complete -c pplhatearch -s f -d "Pass -f to pacman during removal"
complete -c pplhatearch -s h -l help -d "Show help"

# Package arguments fallback to file completion for convenience
complete -c pplhatearch -n "__fish_seen_subcommand_from -S -R" -a "(__fish_complete_path)"
