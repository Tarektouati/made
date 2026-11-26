# fish completion for made.
# Load it with: made completion fish > ~/.config/fish/completions/made.fish

# __made_file_args prints "-f <file>" for a -f/--file value typed before the cursor.
# fish 4 expands the tokens like a run would (variables, ~, globs), but never runs
# command substitutions (commandline -x). Older fish only removes their quoting
# (commandline -o), so a leading ~ is expanded here and variables are left as typed.
function __made_file_args
    set -l tokens
    set -l tilde 0
    if not set tokens (commandline -xpc 2>/dev/null)
        set tokens (commandline -opc)
        set tilde 1
    end
    set -e tokens[1]
    set -l value
    while set -q tokens[1]
        switch $tokens[1]
            case -f -file --file
                if set -q tokens[2]
                    set value $tokens[2]
                end
                set -e tokens[1]
            case '-f=*' '-file=*' '--file=*'
                set value (string replace -r '^[^=]*=' '' -- $tokens[1])
            case -env-file --env-file
                set -e tokens[1]
            case '-*'
            case '*'
                break
        end
        set -e tokens[1]
    end
    set -q value[1]; or return
    if test $tilde = 1
        set value (string replace -r '^~' -- $HOME $value)
    end
    printf '%s\n' -f $value
end

# __made_positional prints the positional words typed before the cursor. Go's flag
# package stops at the first one, so everything from there on is positional.
function __made_positional
    set -l tokens (commandline -opc)
    set -e tokens[1]
    while set -q tokens[1]
        switch $tokens[1]
            case -f -file --file -env-file --env-file
                set -e tokens[1]
            case '-*'
            case '*'
                printf '%s\n' $tokens
                return
        end
        set -e tokens[1]
    end
end

function __made_no_positional
    test (count (__made_positional)) -eq 0
end

# __made_check_mode succeeds when --check, --strict or --host is on the line before
# the first positional word: --check takes no task, and the other two need --check.
function __made_check_mode
    set -l tokens (commandline -opc)
    set -e tokens[1]
    while set -q tokens[1]
        switch $tokens[1]
            case -f -file --file -env-file --env-file
                set -e tokens[1]
            case -check --check -strict --strict -host --host '-check=*' '--check=*' '-strict=*' '--strict=*' '-host=*' '--host=*'
                return 0
            case '-*'
            case '*'
                return 1
        end
        set -e tokens[1]
    end
    return 1
end

# __made_task_start succeeds where a task name, init, completion or self-update may come.
function __made_task_start
    __made_no_positional; and not __made_check_mode
end

# __made_list_names runs made --list-names, which gives up parsing after 1.5s;
# timeout(1), where present, adds a 2s wall-time limit on top.
function __made_list_names
    set -l fileargs (__made_file_args)
    if type -q timeout
        timeout 2 made $fileargs --list-names 2>/dev/null
    else
        command made $fileargs --list-names 2>/dev/null
    end
end

function __made_tasks
    __made_list_names
end

# __made_in_task succeeds once a task name is on the line; its arguments are files.
function __made_in_task
    set -l pos (__made_positional)
    set -q pos[1]; and not contains -- $pos[1] init completion self-update
end

# __made_after_init succeeds after `init` until --force has been given.
function __made_after_init
    set -l pos (__made_positional)
    test "$pos[1]" = init; and not contains -- --force $pos[2..]
end

# __made_after_self_update succeeds after `self-update` until --dry-run has been given.
function __made_after_self_update
    set -l pos (__made_positional)
    test "$pos[1]" = self-update; and not contains -- --dry-run $pos[2..]
end

function __made_completion_shell
    set -l pos (__made_positional)
    test (count $pos) -eq 1; and test $pos[1] = completion
end

complete -c made -f
complete -c made -n __made_no_positional -s f -l file -r -F -d 'Markdown file to read tasks from'
complete -c made -n __made_no_positional -l no-chdir -d 'Run the task in the current directory'
complete -c made -n __made_task_start -l env-file -r -F -d 'Load missing task environment variables'
complete -c made -n __made_task_start -l print -d 'Print the command and script without running'
complete -c made -n __made_task_start -l dry-run -d 'Print the command and script without running'
complete -c made -n __made_no_positional -l check -d 'Check the task file for problems'
complete -c made -n __made_no_positional -l strict -d 'With --check, fail on warnings too'
complete -c made -n __made_no_positional -l host -d 'With --check, also check that interpreters are on PATH'
complete -c made -n __made_task_start -s l -l list -d 'List available tasks'
complete -c made -n __made_task_start -s V -l version -d 'Print version and exit'
complete -c made -n __made_no_positional -s h -l help -d 'Show help'
complete -c made -n __made_task_start -a '(__made_tasks)'
complete -c made -n __made_task_start -a init -d 'Create a starter Madefile.md'
complete -c made -n __made_task_start -a completion -d 'Print a shell completion script'
complete -c made -n __made_task_start -a self-update -d 'Update made to the latest release'
complete -c made -n __made_completion_shell -a 'bash zsh fish'
complete -c made -n __made_after_init -l force -d 'Overwrite an existing Madefile'
complete -c made -n __made_after_self_update -l dry-run -d 'Print what would happen without changing anything'
complete -c made -n __made_in_task -F
