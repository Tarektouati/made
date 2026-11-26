# bash completion for made.
# Load it with: eval "$(made completion bash)"   (e.g. in ~/.bashrc)

# __made_split_words sets __made_words, __made_cword, __made_cur and __made_prev
# (locals of the caller) from COMP_WORDS, with words that bash 4+ split on ":", "="
# or "@" (COMP_WORDBREAKS) joined back, so "ns:task" or "--file=x" is one word. Words
# are joined when nothing separates them in COMP_LINE.
__made_split_words() {
    local line=${COMP_LINE:0:COMP_POINT} trimmed w i n
    __made_words=()
    for ((i = 0; i <= COMP_CWORD; i++)); do
        w=${COMP_WORDS[i]}
        trimmed=${line#"${line%%[![:space:]]*}"}
        n=${#__made_words[@]}
        if ((n)) && ((${#trimmed} == ${#line})) &&
            [[ $w == [:=@] || ${__made_words[n-1]} == *[:=@] ]]; then
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

# __made_comp_word sets __made_ctx and __made_keep (locals of _made) from the word
# being completed, as typed. Readline replaces only part of it: the text after a quote
# left open by the cursor, or else the text after the last unquoted ":" or "=", or from
# the last unquoted "@", in COMP_WORDBREAKS. __made_keep is the part it leaves, and
# __made_ctx the open quote.
__made_comp_word() {
    local w=$1 c q="" i=0 qi=0 brk=0
    while ((i < ${#w})); do
        c=${w:i:1}
        i=$((i + 1))
        if [[ $q == \' ]]; then
            if [[ $c == \' ]]; then
                q=""
            fi
        elif [[ -z $q && $c == [\'\"] ]]; then
            q=$c
            qi=$((i - 1))
        elif [[ $c == \" ]]; then
            q=""
        elif [[ $c == \\ ]]; then
            i=$((i + 1))
        elif [[ -z $q && $c == [:=] && ${COMP_WORDBREAKS-} == *"$c"* ]]; then
            brk=$i
        elif [[ -z $q && $c == @ && ${COMP_WORDBREAKS-} == *@* ]]; then
            brk=$((i - 1))
        fi
    done
    __made_ctx=$q
    if [[ -n $q ]]; then
        __made_keep=${w:0:qi}
    else
        __made_keep=${w:0:brk}
    fi
}

# __made_quote sets __made_q to a task name (or its end) quoted so the shell reads it
# back as one literal word once readline inserts it: with %q, or, after an open quote
# ($1), escaped for that quote. Readline then adds the closing quote, unless the text
# already ends with that character, so it is added here in that case. A "!" inside
# double quotes is history-expanded, so it goes in single quotes.
__made_quote() {
    local i c p out
    if [[ -z $1 ]]; then
        printf -v __made_q '%q' "$2"
        # bash 3.2 leaves a ~ unescaped at the start and after = or :, where bash
        # expands it (a=~root). A loop, as ${q//...} treats a backslash in the
        # replacement differently across bash versions.
        if [[ $__made_q == "~"* || $__made_q == *[=:]"~"* ]]; then
            out= p==
            for ((i = 0; i < ${#__made_q}; i++)); do
                c=${__made_q:i:1}
                if [[ $c == "~" && $p == [=:] ]]; then
                    out=$out\\
                fi
                out=$out$c
                p=$c
            done
            __made_q=$out
        fi
        return
    fi
    __made_q=
    for ((i = 0; i < ${#2}; i++)); do
        c=${2:i:1}
        if [[ $1 == \' && $c == \' ]]; then
            __made_q=$__made_q\'\\\'\'
        elif [[ $1 == \" && $c == [\\\"\$\`] ]]; then
            __made_q=$__made_q\\$c
        elif [[ $1 == \" && $c == ! ]]; then
            __made_q=$__made_q\"\'!\'\"
        else
            __made_q=$__made_q$c
        fi
    done
    if [[ ${__made_q: -1} == "$1" ]]; then
        __made_q=$__made_q$1
    fi
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
    elif [[ -n ${HOME+set} ]]; then
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

# _made also runs in shells with set -e or set -u, so no statement may fail: compgen
# returns 1 when nothing matches, compopt is missing before bash 4.0 and made fails
# without a task file. bash 3.2 reads an empty "${a[@]}" as unset, hence ${a[@]+...}.
_made() {
    local -a __made_words=() __made_fileargs=()
    local __made_cword __made_cur __made_prev
    local __made_i __made_w __made_first="" __made_firstidx=0 __made_checkmode=0
    COMPREPLY=()
    __made_split_words

    # Scan the words before the cursor for a -f/--file value, the --check flags and the
    # first positional word. Go's flag package stops at the first non-flag, like this
    # loop. __made_split_words has joined "--file=x" back if bash split it on "=". A
    # separate "=" word was typed with spaces, and Go reads it as the file name.
    for ((__made_i = 1; __made_i < __made_cword; __made_i++)); do
        __made_w=${__made_words[__made_i]}
        case $__made_w in
            -check | --check | -strict | --strict | -host | --host | \
                -check=* | --check=* | -strict=* | --strict=* | -host=* | --host=*)
                __made_checkmode=1
                ;;
            -f | -file | --file)
                if ((__made_i + 1 < __made_cword)); then
                    __made_fileargs=(-f "$(__made_file_value "${__made_words[__made_i+1]}")")
                fi
                __made_i=$((__made_i + 1))
                ;;
            -f=* | -file=* | --file=*) __made_fileargs=(-f "$(__made_file_value "${__made_w#*=}")") ;;
            -timeout | --timeout) __made_i=$((__made_i + 1)) ;;
            -*) ;;
            *)
                __made_first=$__made_w
                __made_firstidx=$__made_i
                break
                ;;
        esac
    done

    if [[ -n $__made_first ]]; then
        # After `completion`, offer the shells; after `init`, its --force flag, and
        # after `self-update`, its --dry-run flag (each once). None takes file names,
        # so turn off the -o default fallback where bash supports it (4.0+). After a
        # task name, the arguments belong to the task, so file names are the right
        # fallback.
        case $__made_first in
            completion)
                compopt +o default 2>/dev/null || :
                if ((__made_cword == __made_firstidx + 1)); then
                    COMPREPLY=($(compgen -W "bash zsh fish" -- "$__made_cur")) || :
                fi
                ;;
            init)
                compopt +o default 2>/dev/null || :
                for ((__made_i = __made_firstidx + 1; __made_i < __made_cword; __made_i++)); do
                    case ${__made_words[__made_i]} in
                        -force | --force | -force=* | --force=*) return 0 ;;
                    esac
                done
                COMPREPLY=($(compgen -W "--force" -- "$__made_cur")) || :
                ;;
            self-update)
                compopt +o default 2>/dev/null || :
                for ((__made_i = __made_firstidx + 1; __made_i < __made_cword; __made_i++)); do
                    case ${__made_words[__made_i]} in
                        -dry-run | --dry-run | -dry-run=* | --dry-run=*) return 0 ;;
                    esac
                done
                COMPREPLY=($(compgen -W "--dry-run" -- "$__made_cur")) || :
                ;;
        esac
        return 0
    fi

    case $__made_prev in
        -f | -file | --file) return 0 ;; # file names, via complete -o default
        -timeout | --timeout)
            compopt +o default 2>/dev/null || :
            return 0
            ;;
    esac

    # --check takes no task, and --strict and --host need --check: offer only the
    # flags --check accepts, and no file names.
    if ((__made_checkmode)); then
        compopt +o default 2>/dev/null || :
        if [[ $__made_cur == -* ]]; then
            COMPREPLY=($(compgen -W "-f --file --no-chdir --check --strict --host -h --help" -- "$__made_cur")) || :
        fi
        return 0
    fi

    if [[ $__made_cur == -* ]]; then
        COMPREPLY=($(compgen -W "-f --file --no-chdir --timeout --check --strict --host -l --list -V --version -h --help" -- "$__made_cur")) || :
        return 0
    fi

    # Match names against the word with its quoting removed, and reply with the part
    # readline replaces, quoted: names come from the task file and may hold shell syntax.
    local __made_name __made_full __made_kept __made_ctx __made_keep __made_q
    __made_comp_word "$__made_cur"
    __made_full=$(__made_dequote "$__made_cur")
    __made_kept=$(__made_dequote "$__made_keep")
    while IFS=$'\t' read -r __made_name _; do
        if [[ $__made_name == "$__made_full"* ]]; then
            __made_quote "$__made_ctx" "${__made_name#"$__made_kept"}"
            COMPREPLY+=("$__made_q")
        fi
    done < <(__made_list_names ${__made_fileargs[@]+"${__made_fileargs[@]}"} || :; printf '%s\n' init completion self-update)
    return 0
}

complete -o default -F _made made
