import re
rx=re.compile(u"([\u2e80-\uffff])", re.UNICODE)

# find out the terminal width
try:
    import fcntl, termios, struct, os, pty
    (lines, cols) = struct.unpack('hh',
                                  fcntl.ioctl(pty.STDOUT_FILENO,
                                              termios.TIOCGWINSZ,
                                              '1234'))
    if cols == 0:
        raise Exception("Terminal doesn't support TIOCGWINSZ")

    if (cols > 20):
        # I think this looks a bit better with normal-sized terminals
        termwidth = cols - 5
    else:
        termwidth = cols
except:
    termwidth = 75

# Unicode-safe line wrapping function
# from http://aspn.activestate.com/ASPN/Cookbook/Python/Recipe/358117
def wrap(text, width=termwidth, encoding="utf8"):
    return reduce(lambda line, word, width=width: '%s%s%s' %
                  (line,
                   [' ','\n',''][(len(line)-line.rfind('\n')-1
                                  + len(word.split('\n',1)[0] ) >= width) or
                                 line[-1:] == '\0' and 2],
                   word),
                  rx.sub(r'\1\0 ', unicode(text,encoding)).split(' ')
                  ).replace('\0', '').encode(encoding)
