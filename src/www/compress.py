#!/usr/bin/env python
#
# Compresses Javascript/CSS.

import re, sys, os
from optparse import OptionParser

class JavascriptCompressor(object):

    def __init__(self, compressionLevel=2, removeLineBreaks=False):
        self.compressionLevel = compressionLevel
        self.removeLineBreaks = removeLineBreaks

    # a bunch of regexes used in compression
    # first, exempt string literals from compression by transient substitution
    findLiterals = re.compile(r'(\'.*?(?<=[^\\])\')|(\".*?(?<=[^\\])\")')
    stringMarker = '@s@t@r@'
    backSubst = re.compile(stringMarker)        # put the string literals back in

    mlc = re.compile(r'/\*.*?\*\/', re.DOTALL)  # remove multiline comments
    slc = re.compile('\/\/.*')                  # remove single line comments

    collapseWs = re.compile('(?<=\S)[ \t]+')    # collapse successive non-leading white space characters into one
    squeeze = re.compile(
        '''
        \s+(?=[\}\]\)])     |       # remove whitespace including linebreaks preceding closing brackets or braces
        (?<=[\{\;\,\(])\s+  |       # ... or following such
        [ \t]+(?=\W)        |       # remove spaces or tabs preceding non-word characters
        (?<=\W)[ \t]+               # ... or following such. This also will remove leading whitespace
        '''
        , re.DOTALL | re.VERBOSE)

    def compress(self, script):
        '''
        perform compression and return compressed script
        '''
        if not self.compressionLevel:
            return script

        # first, substitute string literals by placeholders to prevent the regexes messing with them
        literals = []

        def insertMarker(mo):
            literals.append(mo.group())
            return self.stringMarker

        script = self.findLiterals.sub(insertMarker, script)

        # now, to the literal-free carcass, apply some kludgy regexes for deflation...
        script = self.slc.sub('', script)       # remove single line comments
        script = self.mlc.sub('\n', script)     # replace multiline comments by newlines
        script = self.collapseWs.sub(' ', script)   # collapse multiple whitespace characters

        # remove empty lines and trailing whitespace
        script = '\n'.join([l.rstrip() for l in script.splitlines() if l.strip()])

        if self.compressionLevel > 1:
            script = self.squeeze.sub('', script)       # squeeze out any dispensible white space
        
        if self.removeLineBreaks:
            script = script.replace('\n', '')           # get rid of any new lines

        # now back-substitute the string literals
        literals.reverse()
        script = self.backSubst.sub(lambda mo: literals.pop(), script)

        return script

if __name__ == "__main__":
    parser = OptionParser(version="%prog 1.0");
    parser.add_option('-n', '--nolinebreaks',
                        dest='linebreaks',
                        action='store_true',
                        default=False,
                        help="remove all line breaks in the resulting file [default: %default]")
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
    
    
    parser.add_option('-o', '--output',
                        dest='output',
                        help="file to save resulting compressed javascript file(s) to")
    parser.add_option('-l', '--level',
                        dest='compression',
                        default=1,
                        type='int',
                        help="level of compression (0 is none, 2 is maximum) [default: %default]")

    (options, args) = parser.parse_args()
    
    if options.compression > 2 or options.compression < 0:
        print "Level of compression must be not be less than zero or more than two."
        sys.exit(1)
    
    if options.output and os.path.exists(options.output):
        if options.quiet:
            print "Warning: output file already exists - overwriting."
        os.remove(options.output)
    
    lengthBefore = 0
    lengthAfter = 0
    c = JavascriptCompressor(compressionLevel=options.compression, removeLineBreaks=options.linebreaks)

    for arg in args:
        fn = arg
        if not options.quiet:
            print "Compressing " + fn + "..."
        try:
            f = open(fn)
            destfn = fn + '.compressed'
            writemode = 'w'
            if options.output:
                destfn = options.output
                writemode = 'a'
            r = open(destfn, writemode)
            try:
                data = f.read()
                lengthBefore += float(len(data))
                cpr = c.compress(data)
                lengthAfter += float(len(cpr))
                r.write(cpr)
                r.close()
            finally:
                f.close()
                r.close()
        except:
            if not options.quiet:
                print "Warning: failed to compress file " + fn + "."
    
    if not options.quiet:    
        if options.measure:
            # Print stats.
            squeezeRate = int(100*(1-(lengthAfter/lengthBefore)))
            kbsize = round((lengthBefore - lengthAfter) / 1024, 2)
            print
            print "Complete! (overall size reduction of %s" % squeezeRate + "% - " + str(kbsize) + "KB)"
        elif lengthAfter != 0 and lengthBefore != 0:
            print
            print "Complete!"
	else:
	    print "Error: no input files specified."
