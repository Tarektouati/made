#compdef made
# zsh completion for made.
# Load it with: eval "$(made completion zsh)"   (after compinit, e.g. in ~/.zshrc)
# or save it as _made in a directory on $fpath.

# _made_list_names runs made --list-names, which gives up parsing after 1.5s;
# timeout(1), where present, adds a 2s wall-time limit on top.
_made_list_names() {
  if (( $+commands[timeout] )); then
    command timeout 2 made "$@" --list-names 2>/dev/null
  else
    command made "$@" --list-names 2>/dev/null
  fi
}

# _made_file_value sets __made_reply to a -f value as typed ($1) with its quoting
# removed and a leading ~, $NAME or ${NAME} expanded; a variable only outside single
# quotes. Nothing else is expanded: NAME must be a plain variable name, as ${(P)NAME}
# runs the code in a subscript like a[$(cmd)]. ${(P)NAME} sees the locals of the
# functions that are running, so these and those of _made_tasks start with __made_ to
# keep user variables visible.
_made_file_value() {
  local __made_rest=${1#\"} __made_name __made_ref
  __made_reply=${(Q)1}
  if [[ $__made_rest == \$\{* ]]; then
    __made_name=${${__made_rest#\$\{}%%\}*}
    __made_ref="{$__made_name}"
  elif [[ $__made_rest == \$* ]]; then
    __made_name=${${__made_rest#\$}%%[^A-Za-z0-9_]*}
    __made_ref=$__made_name
  fi
  if [[ $__made_name == [A-Za-z_]* && $__made_name != *[^A-Za-z0-9_]* &&
        $__made_rest == \$$__made_ref* ]]; then
    __made_reply=${(P)__made_name-}${__made_reply#\$$__made_ref}
  else
    __made_reply=${__made_reply/#\~/$HOME}
  fi
}

_made_tasks() {
  local -a __made_fileargs __made_tasks
  local __made_i __made_line __made_name __made_desc __made_reply
  # Forward a -f/--file value typed before the cursor. words holds the text as typed.
  for (( __made_i = 2; __made_i < CURRENT; __made_i++ )); do
    case $words[__made_i] in
      (-f|-file|--file)
        if (( __made_i + 1 < CURRENT )); then
          _made_file_value "$words[__made_i+1]"
          __made_fileargs=(-f "$__made_reply")
        fi
        (( __made_i++ ))
        ;;
      (-f=*|-file=*|--file=*)
        _made_file_value "${words[__made_i]#*=}"
        __made_fileargs=(-f "$__made_reply")
        ;;
    esac
  done

  # _describe reads "name:description" and removes backslashes, so escape "\" and
  # then ":" in both.
  for __made_line in ${(f)"$(_made_list_names "${__made_fileargs[@]}")"}; do
    __made_name=${__made_line%%$'\t'*}
    __made_desc=${__made_line#*$'\t'}
    __made_name=${${__made_name//\\/\\\\}//:/\\:}
    __made_desc=${${__made_desc//\\/\\\\}//:/\\:}
    __made_tasks+=("$__made_name${__made_desc:+:$__made_desc}")
  done
  __made_tasks+=(
    'init:create a starter Madefile.md'
    'completion:print a shell completion script'
    'self-update:update made to the latest release'
  )
  _describe -t tasks 'task' __made_tasks
}

_made() {
  # _arguments -C needs curcontext, state, line and opt_args under these names.
  local curcontext=$curcontext state line __made_ret=1
  typeset -A opt_args

  _arguments -C -S -A '-*' \
    '(-f -file --file)'{-f+,-file=,--file=}'[Markdown file to read tasks from]:file:_files' \
    '--no-chdir[run the task in the current directory]' \
    '(-l --list -V --version 1 *)--check[check the task file for problems]' \
    '(-l --list -V --version 1 *)--strict[with --check, fail on warnings too]' \
    '(-l --list -V --version 1 *)--host[with --check, also check that interpreters are on PATH]' \
    '(-l --list)'{-l,--list}'[list available tasks]' \
    '(- *)'{-V,--version}'[print version and exit]' \
    '(- *)'{-h,--help}'[show help]' \
    '1: :->first' \
    '*:: :->args' && __made_ret=0

  case $state in
    (first) _made_tasks && __made_ret=0 ;;
    (args)
      case $words[1] in
        (completion) (( CURRENT == 2 )) && _values 'shell' bash zsh fish powershell && __made_ret=0 ;;
        (init) _arguments '--force[overwrite an existing Madefile]' && __made_ret=0 ;;
        (self-update) _arguments '--dry-run[print what would happen without changing anything]' '1::version: ' && __made_ret=0 ;;
        (*) _files && __made_ret=0 ;;
      esac
      ;;
  esac
  return __made_ret
}

if [[ $zsh_eval_context[-1] == loadautofunc ]]; then
  _made "$@"
else
  compdef _made made
fi
