__all__ = ['FileGenerator']

import re
import sys
import time
import random
import warnings

from bongo.external.email.header import Header
from bongo.external.email.payloads import Payload
import bongo.external.email.generator

from cStringIO import StringIO
UNDERSCORE = '_'
NL = '\n'

fcre = re.compile(r'^From ', re.MULTILINE)

def _is8bitstring(s):
    if isinstance(s, str):
        try:
            unicode(s, 'us-ascii')
        except UnicodeError:
            return True
    return False

class FileGenerator :
    def __init__(self, outfp, manglefrom, maxheaderlen=78) :
        self._fp = outfp
        self._maxheaderlen = maxheaderlen
        self._manglefrom = manglefrom
        self._extra = None

    def clone(self, fp) :
        return self.__class__(fp, self._manglefrom, self._maxheaderlen)

    def flatten(self, msg, unixfrom=False) :
        if unixfrom :
            ufrom = msg.get_unixfrom()
            if not ufrom :
                ufrom = 'From nobody ' + time.ctime(time.time())
            print >> self._fp, ufrom
        self._write(msg)

    def write(self, buf, manglefrom=True) :
        if self._extra :
            buf = self._extra + buf
            self._extra = None

        if manglefrom and self._manglefrom :
            buf = fcre.sub('>From ', buf)

        if len(buf) > 4 :
            extra = buf[-4:] 
            if extra != 'From' :
                self._extra = extra
                buf = buf[:-4]

        self._fp.write(buf)

    def flush(self) :
        if self._extra :
            self._fp.write(self._extra)
            self._extra = None

    def _write(self, msg) :
        self._write_headers(msg)        
        self._dispatch(msg)

    def _dispatch(self, msg):
        # Get the Content-Type: for the message, then try to dispatch to
        # self._handle_<maintype>_<subtype>().  If there's no handler for the
        # full MIME type, then dispatch to self._handle_<maintype>().  If
        # that's missing too, then dispatch to self._writeBody().
        main = msg.get_content_maintype()
        sub = msg.get_content_subtype()
        specific = UNDERSCORE.join((main, sub)).replace('-', '_')
        meth = getattr(self, '_handle_' + specific, None)
        if meth is None:
            generic = main.replace('-', '_')
            meth = getattr(self, '_handle_' + generic, None)
            if meth is None:
                meth = self._writeBody
        meth(msg)
        self.flush()

    #
    # Default handlers
    #

    def _write_headers(self, msg, trailing_newline=True):
        self.flush()
        if msg.get_content_maintype() == "multipart" :
            boundary = msg.get_boundary()
            if not boundary :
                msg.set_boundary(bongo.external.email.generator._make_boundary())
            
        for h, v in msg.items():
            print >> self._fp, '%s:' % h,
            if self._maxheaderlen == 0:
                # Explicit no-wrapping
                print >> self._fp, v
            elif isinstance(v, Header):
                # Header instances know what to do
                print >> self._fp, v.encode()
            elif _is8bitstring(v):
                # If we have raw 8bit data in a byte string, we have no idea
                # what the encoding is.  There is no safe way to split this
                # string.  If it's ascii-subset, then we could do a normal
                # ascii split, but if it's multibyte then we could break the
                # string.  There's no way to know so the least harm seems to
                # be to not split the string and risk it being too long.
                print >> self._fp, v
            else:
                # Header's got lots of smarts, so use it.
                print >> self._fp, Header(
                    v, maxlinelen=self._maxheaderlen,
                    header_name=h, continuation_ws='\t').encode()
        # A blank line always separates headers from body
        if trailing_newline :
            print >> self._fp

    #
    # Handlers for writing types and subtypes
    #

    def _handle_text(self, msg):
        for buf in msg.iter_payload() :
            self.write(buf, True)

    # Default body handler
    _writeBody = _handle_text
    
    def _handle_multipart(self, msg):
        subparts = msg.get_payload_obj()
        
        boundary = msg.get_boundary(failobj=bongo.external.email.generator._make_boundary())
        
        if subparts is None :
            subparts = []
        elif isinstance(subparts, Payload) :
            self._handle_text(msg)
            return

        # FIXME: fix bad boundaries
        start = self._fp.tell()

        self.flush()
        if msg.preamble is not None :
            print >> self._fp, msg.preamble
                
        print >> self._fp, '--' + boundary
        
        if isinstance(subparts, list) :
            first = True
            for part in subparts :
                if not first:
                    self.flush()
                    print >> self._fp, NL + '--' + boundary
                first = False
                    
                g = self.clone(self._fp)
                g.flatten(part)
        self.write(NL + '--' + boundary + '--')
        
        if msg.epilogue is not None :
            self.flush()
            print >> self._fp
            self.write(msg.epilogue, True)
    
    def _strip_newline(self) :
        pass
    
    def _handle_message_delivery_status(self, msg):
        # We can't just write the headers directly to self's file object
        # because this will leave an extra newline between the last header
        # block and the boundary.  Sigh.
        blocks = []
        for part in msg.get_payload():            
            s = StringIO()
            g = self.clone(s)
            g.flatten(part, unixfrom=False)
            text = s.getvalue()
            lines = text.split('\n')
            # Strip off the unnecessary trailing empty line
            if lines and lines[-1] == '':
                blocks.append(NL.join(lines[:-1]))
            else:
                blocks.append(text)
        # Now join all the blocks with an empty line.  This has the lovely
        # effect of separating each block with an empty line, but not adding
        # an extra one after the last one.
        self.write(NL.join(blocks), True)

    def _handle_message(self, msg):
        g = self.clone(self._fp)
        # The payload of a message/rfc822 part should be a multipart sequence
        # of length 1.  The zeroth element of the list should be the Message
        # object for the subpart.  Extract that object, stringify it, and
        # write it out.
        g.flatten(msg.get_payload(0), unixfrom=False)


