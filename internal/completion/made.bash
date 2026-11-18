# bash completion for made.
# Load it with: eval "$(made completion bash)"   (e.g. in ~/.bashrc)

# __made_words sets words, cword, cur and prev (locals of the caller) from COMP_WORDS,
# with words that bash split on ":" (COMP_WORDBREAKS) joined back, so a task name like
# "ns:task" is one word. Words are joined when nothing separates them in COMP_LINE.
__made_words() {
    local line=${COMP_LINE:0:COMP_POINT} trimmed w i n
    words=()
    for ((i = 0; i <= COMP_CWORD; i++)); do
        w=${COMP_WORDS[i]}
        trimmed=${line#"${line%%[![:space:]]*}"}
        n=${#words[@]}
        if ((n)) && ((${#trimmed} == ${#line})) &&
            [[ $w == : || ${words[n-1]} == *: ]]; then
            words[n-1]=${words[n-1]}$w
        else
            words[n]=$w
        fi
        line=${trimmed#"$w"}
    done
    cword=$((${#words[@]} - 1))
    cur=${words[cword]}
    prev=
    if ((cword > 0)); then
        prev=${words[cword-1]}
    fi
}

# __made_dequote prints a word as typed with its shell quoting removed: '...', "..."
# and backslash escapes. It never evaluates the word.
__made_dequote() {
    local w=$1 out="" c dq=0
    case $w in
        \'*)
            w=${w#\'}
            printf '%s' "${w%\'}"
            return
            ;;
        \"*)
            w=${w#\"}
            w=${w%\"}
            dq=1
            ;;
    esac
    while [[ -n $w ]]; do
        c=${w:0:1}
        w=${w:1}
        if [[ $c == \\ && -n $w ]]; then
            # Inside double quotes, a backslash only escapes " \ $ and `.
            if ((dq)) && [[ ${w:0:1} != [\"\\\$\`] ]]; then
                out=$out$c
            fi
            c=${w:0:1}
            w=${w:1}
        fi
        out=$out$c
    done
    printf '%s' "$out"
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
    local -a words=() fileargs=()
    local cword cur prev
    local i w first="" firstidx=0
    COMPREPLY=()
    __made_words

    # Scan the words before the cursor for a -f/--file value and the first positional
    # word. Go's flag package stops at the first non-flag, like this loop. bash splits
    # "--file=x" into "--file" "=" "x" by default (COMP_WORDBREAKS), so handle both.
    for ((i = 1; i < cword; i++)); do
        w=${words[i]}
        case $w in
            -f | -file | --file)
                if [[ ${words[i+1]} == "=" ]]; then
                    ((i++))
                fi
                if ((i + 1 < cword)); then
                    fileargs=(-f "$(__made_dequote "${words[i+1]}")")
                fi
                ((i++))
                ;;
            -f=* | -file=* | --file=*) fileargs=(-f "$(__made_dequote "${w#*=}")") ;;
            -*) ;;
            *)
                first=$w
                firstidx=$i
                break
                ;;
        esac
    done
    if ((${#fileargs[@]})); then
        fileargs[1]=${fileargs[1]/#\~/$HOME}
    fi

    if [[ -n $first ]]; then
        # After `completion`, offer the shells; after `init`, its --force flag (once).
        # Neither takes file names, so turn off the -o default fallback where bash
        # supports it (4.0+). After a task name, the arguments belong to the task,
        # so file names are the right fallback.
        case $first in
            completion)
                compopt +o default 2>/dev/null
                if ((cword == firstidx + 1)); then
                    COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur"))
                fi
                ;;
            init)
                compopt +o default 2>/dev/null
                for ((i = firstidx + 1; i < cword; i++)); do
                    case ${words[i]} in
                        -force | --force | -force=* | --force=*) return 0 ;;
                    esac
                done
                COMPREPLY=($(compgen -W "--force" -- "$cur"))
                ;;
        esac
        return 0
    fi

    case $prev in
        -f | -file | --file) return 0 ;; # file names, via complete -o default
        =)
            case ${words[cword-2]} in
                -f | -file | --file) return 0 ;;
            esac
            ;;
    esac

    if [[ $cur == -* ]]; then
        COMPREPLY=($(compgen -W "-f --file --no-chdir --check --strict -l --list -V --version -h --help" -- "$cur"))
        return 0
    fi

    # bash replaces only the text after the last ":" in cur, so drop what comes before
    # it from each match.
    local name prefix=""
    if [[ $cur == *:* && $COMP_WORDBREAKS == *:* ]]; then
        prefix=${cur%"${cur##*:}"}
    fi
    while IFS=$'\t' read -r name _; do
        [[ $name == "$cur"* ]] && COMPREPLY+=("${name#"$prefix"}")
    done < <(__made_list_names "${fileargs[@]}"; printf '%s\n' init completion)
    return 0
}

complete -o default -F _made made
