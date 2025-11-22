#compdef pplhatearch

_pplhatearch() {
  local -a args opts
  opts=(
    '(-S -R)-S[Install (clone/build) packages]'
    '(-S -R)-R[Remove packages]'
    '-Ss[Search backup AUR mirror]'
    '-Si[Show package information]'
    '-Q[Show information about installed packages via pacman]'
    '-C[Directory to use for cache clones]:directory:_files -/'
    '-repo[Override Git mirror URL]:url:_urls'
    '-color[Enable or disable colored output]'
    '-n[Pass -n to pacman during removal]'
    '-s[Pass -s to pacman during removal]'
    '-d[Pass -d to pacman during removal]'
    '-f[Pass -f to pacman during removal]'
    '-h[Show help]'
    '--help[Show help]'
  )

  _arguments -s -S : "${opts[@]}" '*:package name:_files'
}

_pplhatearch "$@"
