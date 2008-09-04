#!/usr/bin/env python
#
# compress.py
# Compresses Javascript/CSS (what else do you think it does?).

import re, sys, os, traceback
from optparse import OptionParser
from time import time
from StringIO import StringIO

# The following code is original from jsmin by Douglas Crockford, it was translated to
# Python by Baruch Even. The following code had the following copyright and
# license.
#
# /* jsmin.c
#    2007-05-22
#
# Copyright (c) 2002 Douglas Crockford  (www.crockford.com)
#
# Permission is hereby granted, free of charge, to any person obtaining a copy of
# this software and associated documentation files (the "Software"), to deal in
# the Software without restriction, including without limitation the rights to
# use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
# of the Software, and to permit persons to whom the Software is furnished to do
# so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# The Software shall be used for Good, not Evil.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.
# */

def jsmin(js):
    ins = StringIO(js)
    outs = StringIO()
    JavascriptMinify().minify(ins, outs)
    str = outs.getvalue()
    if len(str) > 0 and str[0] == '\n':
        str = str[1:]
    return str

def isAlphanum(c):
    """return true if the character is a letter, digit, underscore,
           dollar sign, or non-ASCII character.
    """
    return ((c >= 'a' and c <= 'z') or (c >= '0' and c <= '9') or
            (c >= 'A' and c <= 'Z') or c == '_' or c == '$' or c == '\\' or (c is not None and ord(c) > 126));

class UnterminatedComment(Exception):
    pass

class UnterminatedStringLiteral(Exception):
    pass

class UnterminatedRegularExpression(Exception):
    pass

class JavascriptMinify(object):

    def _outA(self):
        self.outstream.write(self.theA)
    def _outB(self):
        self.outstream.write(self.theB)

    def _get(self):
        """return the next character from stdin. Watch out for lookahead. If
           the character is a control character, translate it to a space or
           linefeed.
        """
        c = self.theLookahead
        self.theLookahead = None
        if c == None:
            c = self.instream.read(1)
        if c >= ' ' or c == '\n':
            return c
        if c == '': # EOF
            return '\000'
        if c == '\r':
            return '\n'
        return ' '

    def _peek(self):
        self.theLookahead = self._get()
        return self.theLookahead

    def _next(self):
        """get the next character, excluding comments. peek() is used to see
           if a '/' is followed by a '/' or '*'.
        """
        c = self._get()
        if c == '/':
            p = self._peek()
            if p == '/':
                c = self._get()
                while c > '\n':
                    c = self._get()
                return c
            if p == '*':
                c = self._get()
                while 1:
                    c = self._get()
                    if c == '*':
                        if self._peek() == '/':
                            self._get()
                            return ' '
                    if c == '\000':
                        raise UnterminatedComment()

        return c

    def _action(self, action):
        """do something! What you do is determined by the argument:
           1   Output A. Copy B to A. Get the next B.
           2   Copy B to A. Get the next B. (Delete A).
           3   Get the next B. (Delete B).
           action treats a string as a single character. Wow!
           action recognizes a regular expression if it is preceded by ( or , or =.
        """
        if action <= 1:
            self._outA()

        if action <= 2:
            self.theA = self.theB
            if self.theA == "'" or self.theA == '"':
                while 1:
                    self._outA()
                    self.theA = self._get()
                    if self.theA == self.theB:
                        break
                    if self.theA <= '\n':
                        raise UnterminatedStringLiteral()
                    if self.theA == '\\':
                        self._outA()
                        self.theA = self._get()


        if action <= 3:
            self.theB = self._next()
            if self.theB == '/' and (self.theA == '(' or self.theA == ',' or
                                     self.theA == '=' or self.theA == ':' or
                                     self.theA == '[' or self.theA == '?' or
                                     self.theA == '!' or self.theA == '&' or
                                     self.theA == '|' or self.theA == ';' or
                                     self.theA == '{' or self.theA == '}' or
                                     self.theA == '\n'):
                self._outA()
                self._outB()
                while 1:
                    self.theA = self._get()
                    if self.theA == '/':
                        break
                    elif self.theA == '\\':
                        self._outA()
                        self.theA = self._get()
                    elif self.theA <= '\n':
                        raise UnterminatedRegularExpression()
                    self._outA()
                self.theB = self._next()


    def _jsmin(self):
        """Copy the input to the output, deleting the characters which are
           insignificant to JavaScript. Comments will be removed. Tabs will be
           replaced with spaces. Carriage returns will be replaced with linefeeds.
           Most spaces and linefeeds will be removed.
        """
        self.theA = '\n'
        self._action(3)

        while self.theA != '\000':
            if self.theA == ' ':
                if isAlphanum(self.theB):
                    self._action(1)
                else:
                    self._action(2)
            elif self.theA == '\n':
                if self.theB in ['{', '[', '(', '+', '-']:
                    self._action(1)
                elif self.theB == ' ':
                    self._action(3)
                else:
                    if isAlphanum(self.theB):
                        self._action(1)
                    else:
                        self._action(2)
            else:
                if self.theB == ' ':
                    if isAlphanum(self.theA):
                        self._action(1)
                    else:
                        self._action(3)
                elif self.theB == '\n':
                    if self.theA in ['}', ']', ')', '+', '-', '"', '\'']:
                        self._action(1)
                    else:
                        if isAlphanum(self.theA):
                            self._action(1)
                        else:
                            self._action(3)
                else:
                    self._action(1)

    def minify(self, instream, outstream):
        self.instream = instream
        self.outstream = outstream
        self.theA = '\n'
        self.theB = None
        self.theLookahead = None

        self._jsmin()
        self.instream.close()

# The following code is (c) 2007 Alexander Hixon <hixon.alexander@mediati.org>
# See COPYING for more details.

if __name__ == "__main__":
    desc = "Compresses Javascript and CSS files, so that web applications can have lower download times and save bandwidth. This tool mainly removes unnecessary whitespace and removes comments; it does not modify quoted strings, regular expression literals or variable names."
    parser = OptionParser(usage="%prog [options] inputfile ...", version="%prog 1.0", description=desc);
    parser.add_option('-n', '--no-linebreaks',
                        dest='linebreaks',
                        action='store_true',
                        default=False,
                        help="remove all line breaks in the resulting file (not valid for Javascript) [default: %default]")
    parser.add_option('-q', '--quiet',
                        dest='quiet',
                        action='store_true',
                        default=False,
                        help="be vewwy quiet!")
    parser.add_option('-m', '--measure',
                        dest='measure',
                        default=False,
                        action='store_true',
                        help="print compression statistics")
    
    parser.add_option('-t', '--type',
                        dest='filetype',
                        default='autodetect',
                        help="type of file ('autodetect', 'js' and 'css' are all valid).")
    parser.add_option('-o', '--output',
                        dest='output',
                        help="file to save resulting compressed file(s) to")
    parser.add_option('-i', '--ignore',
                        dest='ignore',
                        action='append',
                        help="ignore a certain filename")
    parser.add_option('--no-compression',
                        dest='nocompression',
                        default=False,
                        help="don't compress anything")
    parser.add_option('-I', '--include',
                        dest='include_dir',
                        default=None,
                        help="directory to search for source files")

    (options, args) = parser.parse_args()
    
    # Make sure we don't append to an alreaady existing output (that's stupid)
    if options.output and os.path.exists(options.output):
        if options.quiet:
            print "** Warning: output file already exists - overwriting."
        os.remove(options.output)
    
    # Setup our counters for stats later
    lengthBefore = 0
    lengthAfter = 0
    
    # Check for inputs
    if not len(args) > 0:
        if not options.quiet:
            print "Error: no input files specified."
        sys.exit(1)

    # Make sure the user doesn't want to measure multiple inputs and outputs;
    # we can only handle measuring a single output, with either single or many inputs.
    if options.measure:
        if len(args) > 1 and not options.output:
            if not options.quiet:
                print "Error: unable to measure multiple input files and output files."
                print "       Please specify a single output, or drop the --measure argument."
            sys.exit(1)
    
    # Filetype auto-detection!
    ftype = options.filetype
    if ftype == 'autodetect':
        # Work out the filetype based on the extension of first file
        ext = ""
        try:
            ext = os.path.splitext(args[0])[1]
        except:
            # Fail if we can't get the extention
            if not options.quiet:
                print "Error: Could not determine filetype automatically. Please specify if using"
                print "       the --type argument."
            sys.exit(1)
            
        if ext == '.js':
            ftype = 'js'
        elif ext == '.css':
            ftype = 'css'
        else:
            # Tell the user off for trying something that doesn't look valid.
            print "Error: unsupported filetype or incorrectly autodetected."
            print "       If you're sure that kind of file is supported, force the filetype with"
            print "       the --type argument."
            sys.exit(1)
            
    # If we get here, all is OK!
    
    # Find out when we start (for timer)
    start = time()
    
    # Loop through each input
    for arg in args:
        fn = arg
        if not options.quiet:
            print "Compressing " + fn + "..."
        try:
            if not os.path.exists(fn) and options.include_dir != None:
                fn = options.include_dir + "/" + fn
            
            if not os.path.exists(fn):
                if not options.quiet:
                    print "** Warning: file " + fn + " does not exist."
                continue
            
            if options.ignore and options.ignore.count(fn) > 0:
                if not options.quiet:
                    print "** Warning: skipping file " + fn + "."
                continue
            
            f = open(fn)
            destfn = fn + '.compressed'
            writemode = 'w'
            # Set mode to 'write' if no output was specified (many in-many out), or
            # append if we were given an output (many in-single out).
            if options.output:
                destfn = options.output
                writemode = 'a'
            r = open(destfn, writemode)
            try:
                # Type-based operations
                if options.nocompression:
                    # If the user doesn't actually /want/ to compress anything, just cat the files together.
                    r.write(f.read())
                elif ftype == "js" or ftype == "javascript":
                    c = JavascriptMinify()
                    c.minify(f, r)
                    
                    # Tell use they can't remove line breaks - it borks some JS files!
                    if options.linebreaks and not options.quiet:
                        print "** Warning: --nolinebreaks is not supported on Javascript files."
                elif ftype == "css" or ftype == "stylesheet":
                    css = f.read()
                    css = re.sub(r'/[*]([^*]|(\*[^/]))*[*]/', '', css) # Strip C-style comments
                    css = re.sub(r'\s+', ' ', css)                     # compress whitespace
                    css = css.replace('\t', '')
                    if options.linebreaks:
                        css = css.replace('\n', '')
                    
                    r.write(css)
                else:
                    if not options.quiet:
                        print "Error: unsupported file type."
                    break
            finally:
                # Close streams.
                f.close()
                r.close()
                
                # Update counters.
                lengthBefore += float(os.path.getsize(fn))
                if not options.output:
                    lengthAfter += float(os.path.getsize(destfn))
                else:
                    lengthAfter = float(os.path.getsize(destfn))
        except Exception, ex:
            if not options.quiet:
                print "** Warning: failed to compress file " + fn + "."
                print "Exception: " + str(ex)
    
    # And stop counting.
    stop = time()
    
    if not options.quiet:    
        if options.measure:
            # Print stats.
            squeezeRate = int(100*(1-(lengthAfter/lengthBefore)))
            kbsize = round((lengthBefore - lengthAfter) / 1024, 2)
            timetook = stop - start
            print
            print "  Size before compression : " + str(round(lengthBefore / 1024, 2)) + " KB"
            print "   Size after compression : " + str(round(lengthAfter / 1024, 2)) + " KB"
            print "    Percentage of savings : " + str(squeezeRate) + "%"
            print "               Size saved : " + str(kbsize) + " KB"
            print "            Compressed in : %3.3f" % timetook + " sec"
            print "         Compression rate : " + str(round(kbsize / timetook, 2)) + " KB/sec"
            
        print
        print "Complete."
