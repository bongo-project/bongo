import tempfile
import binascii
import quopri
import uu
import base64
from bongo.external.email import utils

class Payload:
    def __init__(self, encoding=None, value=''):
        self.encoding = encoding
        self.set(value)

    def set(self, buffer):
        raise NotImplementedError

    def get(self, decode):
        return ''.join(self.iter(decode))

    def add(self, chunk):
        raise NotImplementedError

    def strip_eol(self) :
        raise NotImplementedError

    def iter(self, decode):
        raise NotImplementedError

class MemoryPayload(Payload):
    def __init__(self, encoding=None, value='') :
        Payload.__init__(self, encoding, value)

    def set(self, value):
        self._buffer = [value]

    def add(self, chunk):
        self._buffer.append(chunk)

    def strip_eol(self) :
        value = ''.join(self._buffer)
        if value.endswith('\r\n') :
            value = value[:-2]
        elif value.endswith('\r') or value.endswith('\n') :
            value = value[:-1]
        self._buffer = [value]

    def iter(self, decode):
        if decode:
            decoder = get_string_decoder(self.encoding)
        else:
            decoder = None
        
        # Just yield the payload all at once since it's all in memory already
        if decoder:
            yield decoder(''.join(self._buffer))
        else:
            yield ''.join(self._buffer)

class FilePayload(Payload):

    chunk_size = 8192

    def __init__(self, encoding=None, value=''):
        self._f = None
        Payload.__init__(self, encoding, value)

    def __del__(self):
        self._close()

    def _close(self):
        if self._f:
            self._f.close()

    def set(self, buf):
        self._close()
        self._f = tempfile.TemporaryFile()
        self._f.write(buf)

    def add(self, chunk):
        self._f.seek(0, 2)
        self._f.write(chunk)

    def strip_eol(self) :
        self._f.seek(0, 2)
        toRead = self._f.tell()
        if toRead > 2 :
            toRead = 2

        self._f.seek(0 - toRead, 2)
        end = self._f.read(toRead)
        if end == '\r\n' :
            self._f.seek(-2, 2)
            self._f.truncate()
        elif end.endswith('\r') or end.endswith('\n') :
            self._f.seek(-1, 2)
            self._f.truncate()

    def iter(self, decode):
        if decode:
            decoder = get_file_decoder(self.encoding)
        else:
            decoder = None

        if decoder:
            # Decode into a separate file first to ensure there are no decoding
            # errors. 
            fout = tempfile.TemporaryFile()
            self._f.seek(0, 0)
            decoder(self._f, fout)
        else:
            fout = self._f

        # Feed out the payload in chunks
        fout.seek(0, 0)
        while 1:
            buf = fout.read(self.chunk_size)
            if buf:
                yield buf
            else:
                break

        # If decoding occurred, close the temporary file for the decoded version
        if fout != self._f:
            fout.close()
            
def get_string_decoder(cte):
    if cte == None :
        return None
    
    cte = cte.lower()

    if cte == 'quoted-printable':
        return utils._qdecode
    elif cte == 'base64':
        return utils._bdecode
    elif cte in ('x-uuencode', 'uuencode', 'uue', 'x-uue'):
        return binascii.a2b_uu
    else:
        return None

def get_file_decoder(cte):
    if cte == None :
        return None
    
    cte = cte.lower()

    if cte == 'quoted-printable':
        return quopri.decode
    elif cte == 'base64':
        return base64.decode
    elif cte in ('x-uuencode', 'uuencode', 'uue', 'x-uue'):
        return uu.decode
    else:
        return None

