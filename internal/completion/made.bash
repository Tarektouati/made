# bash completion for made.
# Load it with: eval "$(made completion bash)"   (e.g. in ~/.bashrc)

# __made_split_words sets __made_words, __made_cword, __made_cur and __made_prev
# (locals of the caller) from COMP_WORDS, with words that bash split on ":"
# (COMP_WORDBREAKS) joined back, so a task name like "ns:task" is one word. Words are
# joined when nothing separates them in COMP_LINE.
__made_split_words() {
    local line=${COMP_LINE:0:COMP_POINT} trimmed w i n
    __made_words=()
    for ((i = 0; i <= COMP_CWORD; i++)); do
        w=${COMP_WORDS[i]}
        trimmed=${line#"${line%%[![:space:]]*}"}
        n=${#__made_words[@]}
        if ((n)) && ((${#trimmed} == ${#line})) &&
            [[ $w == : || ${__made_words[n-1]} == *: ]]; then
            __made_words[n-1]=${__made_words[n-1]}$w
        else
            __made_words[n]=$w
        fi
        line=${trimmed#"$w"}
    done
    __made_cword=$((${#__made_words[@]} - 1))
    __made_cur=${__made_words[__made_cword]}
    __made_prev=
    if ((__made_cword > 0)); then
        __made_prev=${__made_words[__made_cword-1]}
    fi
}

# __made_dequote prints a word as typed with its shell quoting removed: '...', "..."
# and backslash escapes, anywhere in the word ("$HOME"/x, "my "tasks.md). A quote left
# open by the cursor runs to the end of the word. It never evaluates the word.
__made_dequote() {
    local w=$1 out="" c q=""
    while [[ -n $w ]]; do
        c=${w:0:1}
        w=${w:1}
        if [[ $q == \' ]]; then
            if [[ $c == \' ]]; then
                q=""
            else
                out=$out$c
            fi
        elif [[ -z $q && $c == [\'\"] ]]; then
            q=$c
        elif [[ $c == \" ]]; then
            q=""
        elif [[ $c == \\ && -n $w ]]; then
            # Inside double quotes, a backslash only escapes " \ $ and `.
            if [[ -n $q && ${w:0:1} != [\"\\\$\`] ]]; then
                out=$out$c
            fi
            out=$out${w:0:1}
            w=${w:1}
        else
            out=$out$c
        fi
    done
    printf '%s' "$out"
}

# __made_file_value prints a -f value as typed with its quoting removed and a leading
# ~, $NAME or ${NAME} expanded; a variable only outside single quotes. Nothing else is
# expanded: NAME must be a plain variable name, as ${!NAME} runs the code in a
# subscript like a[$(cmd)]. ${!NAME} sees the locals of the functions that are
# running, so these and those of _made start with __made_ to keep user variables
# visible.
__made_file_value() {
    local __made_re='^"?\$(\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))' __made_v __made_name
    __made_v=$(__made_dequote "$1")
    if [[ $1 =~ $__made_re ]]; then
        __made_name=${BASH_REMATCH[2]}${BASH_REMATCH[3]}
        __made_v=${!__made_name-}${__made_v#"\$${BASH_REMATCH[1]}"}
    else
        __made_v=${__made_v/#\~/$HOME}
    fi
    printf '%s' "$__made_v"
}

# __made_list_names runs made --list-names so untrusted repos cannot stall tab
# completion: made gives up parsing after 1.5s, and timeout(1), where present, adds
# a 2s wall-time limit on top.
__made_list_names() {
    if command -v timeout >/dev/null 2>&1; then
        timeout 2 made "$@" --list-names 2>/dev/null
    else
        made "$@" --list-names 2>/dev/null
    fi
}

_made() {
    local -a __made_words=() __made_fileargs=()
    local __made_cword __made_cur __made_prev
    local __made_i __made_w __made_first="" __made_firstidx=0 __made_checkmode=0
    COMPREPLY=()
    __made_split_words

    # Scan the words before the cursor for a -f/--file value, the --check flags and the
    # first positional word. Go's flag package stops at the first non-flag, like this
    # loop. bash splits "--file=x" into "--file" "=" "x" by default (COMP_WORDBREAKS),
    # so handle both.
    for ((__made_i = 1; __made_i < __made_cword; __made_i++)); do
        __made_w=${__made_words[__made_i]}
        case $__made_w in
            -check | --check | -strict | --strict | -host | --host | \
                -check=* | --check=* | -strict=* | --strict=* | -host=* | --host=*)
                __made_checkmode=1
                ;;
            -f | -file | --file)
                if [[ ${__made_words[__made_i+1]} == "=" ]]; then
                    ((__made_i++))
                fi
                if ((__made_i + 1 < __made_cword)); then
                    __made_fileargs=(-f "$(__made_file_value "${__made_words[__made_i+1]}")")
                fi
                ((__made_i++))
                ;;
            -f=* | -file=* | --file=*) __made_fileargs=(-f "$(__made_file_value "${__made_w#*=}")") ;;
            -*) ;;
            *)
                __made_first=$__made_w
                __made_firstidx=$__made_i
                break
                ;;
        esac
    done

    if [[ -n $__made_first ]]; then
        # After `completion`, offer the shells; after `init`, its --force flag (once).
        # Neither takes file names, so turn off the -o default fallback where bash
        # supports it (4.0+). After a task name, the arguments belong to the task,
        # so file names are the right fallback.
        case $__made_first in
            completion)
                compopt +o default 2>/dev/null
                if ((__made_cword == __made_firstidx + 1)); then
                    COMPREPLY=($(compgen -W "bash zsh fish" -- "$__made_cur"))
                fi
                ;;
            init)
                compopt +o default 2>/dev/null
                for ((__made_i = __made_firstidx + 1; __made_i < __made_cword; __made_i++)); do
                    case ${__made_words[__made_i]} in
                        -force | --force | -force=* | --force=*) return 0 ;;
                    esac
                done
                COMPREPLY=($(compgen -W "--force" -- "$__made_cur"))
                ;;
        esac
        return 0
    fi

    case $__made_prev in
        -f | -file | --file) return 0 ;; # file names, via complete -o default
        =)
            case ${__made_words[__made_cword-2]} in
                -f | -file | --file) return 0 ;;
            esac
            ;;
    esac

    # --check takes no task, and --strict and --host need --check: offer only the
    # flags --check accepts, and no file names.
    if ((__made_checkmode)); then
        compopt +o default 2>/dev/null
        if [[ $__made_cur == -* ]]; then
            COMPREPLY=($(compgen -W "-f --file --no-chdir --check --strict --host -h --help" -- "$__made_cur"))
        fi
        return 0
    fi

    if [[ $__made_cur == -* ]]; then
        COMPREPLY=($(compgen -W "-f --file --no-chdir --check --strict --host -l --list -V --version -h --help" -- "$__made_cur"))
        return 0
    fi

    # bash replaces only the text after the last ":" in the current word, so drop what
    # comes before it from each match.
    local __made_name __made_prefix=""
    if [[ $__made_cur == *:* && $COMP_WORDBREAKS == *:* ]]; then
        __made_prefix=${__made_cur%"${__made_cur##*:}"}
    fi
    while IFS=$'\t' read -r __made_name _; do
        [[ $__made_name == "$__made_cur"* ]] && COMPREPLY+=("${__made_name#"$__made_prefix"}")
    done < <(__made_list_names "${__made_fileargs[@]}"; printf '%s\n' init completion)
    return 0
}

complete -o default -F _made made
