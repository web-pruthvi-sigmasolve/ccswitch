# Record with: vhs demo.tape   (or: make demo)
# Output: docs/demo.gif
# Needs a fixture store so the demo shows no real chats:
#   export CLAUDE_USER_DATA=$PWD/testdata/demo-store

Output docs/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1000
Set Height 600
Set Padding 16
Set TypingSpeed 50ms
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful

# Launch
Type "ccswitch"
Sleep 400ms
Enter
Sleep 2s

# Accounts screen: pick source (work)
Down
Sleep 400ms
Up
Sleep 400ms
Enter
Sleep 1500ms

# Direction screen: pick destination (personal)
Down
Sleep 600ms
Enter
Sleep 1500ms

# Chat picker: select two chats
Down
Sleep 300ms
Space
Sleep 400ms
Down
Sleep 300ms
Down
Sleep 300ms
Space
Sleep 1s
Enter
Sleep 2s

# Review screen (dry run), then apply
Sleep 2s
Enter
Sleep 2500ms

# Done screen
Sleep 2s
Type "q"
Sleep 500ms
