#compdef made
# zsh completion for made.
# Load it with: eval "$(made completion zsh)"   (after compinit, e.g. in ~/.zshrc)
# or save it as _made in a directory on $fpath.

_made_tasks() {
  local -a fileargs tasks
  local i line name desc
  # Forward a -f/--file value typed before the cursor. words holds the text as typed,
  # so (Q) removes its quoting.
  for (( i = 2; i < CURRENT; i++ )); do
    case $words[i] in
      (-f|-file|--file) (( i + 1 < CURRENT )) && fileargs=(-f "${(Q)words[i+1]}"); (( i++ )) ;;
      (-f=*|-file=*|--file=*) fileargs=(-f "${(Q)${words[i]#*=}}") ;;
    esac
  done
  (( $#fileargs )) && fileargs[2]=${fileargs[2]/#\~/$HOME}

  for line in ${(f)"$(command made "${fileargs[@]}" --list-names 2>/dev/null)"}; do
    name=${line%%$'\t'*}
    desc=${line#*$'\t'}
    tasks+=("${name//:/\\:}${desc:+:$desc}")
  done
  tasks+=(
    'init:create a starter Madefile.md'
    'completion:print a shell completion script'
  )
  _describe -t tasks 'task' tasks
}

_made() {
  local curcontext=$curcontext state line ret=1
  typeset -A opt_args

  _arguments -C -S -A '-*' \
    '(-f -file --file)'{-f+,-file=,--file=}'[Markdown file to read tasks from]:file:_files' \
    '--no-chdir[run the task in the current directory]' \
    '(-l --list)'{-l,--list}'[list available tasks]' \
    '(- *)'{-V,--version}'[print version and exit]' \
    '(- *)'{-h,--help}'[show help]' \
    '1: :->first' \
    '*:: :->args' && ret=0

  case $state in
    (first) _made_tasks && ret=0 ;;
    (args)
      case $words[1] in
        (completion) (( CURRENT == 2 )) && _values 'shell' bash zsh fish && ret=0 ;;
        (init) _arguments '--force[overwrite an existing Madefile]' && ret=0 ;;
        (*) _files && ret=0 ;;
      esac
      ;;
  esac
  return ret
}

if [[ $zsh_eval_context[-1] == loadautofunc ]]; then
  _made "$@"
else
  compdef _made made
fi
