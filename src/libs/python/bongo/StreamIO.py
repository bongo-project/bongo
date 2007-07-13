from libbongo.libs import streamio
import cStringIO as StringIO

class Stream:
    def __init__(self, stream):
        self.stream = stream
        
        self.obj = streamio.BongoStreamCreate([], False)
        self.availCount = 0
        self.bufLen = 1024
        self.buf = " " * self.bufLen
        self.eos = False

    def AddCodec(self, codec, encode=False):
        return streamio.BongoStreamAddCodec(self.obj, codec, encode)

    def _FillBuffer(self):
        buf = self.stream.read(self.bufLen)
        if len(buf) == 0:
            self.eos = True
            streamio.BongoStreamEos(self.obj)
            return

        streamio.BongoStreamPut(self.obj, buf)

    def _RefreshBuffer(self):
        if self.availCount > 0:
            return True

        self.availOffset = 0
        self.availCount = streamio.BongoStreamGet(self.obj, self.buf)

        while self.availCount == 0 and not self.eos:
            self._FillBuffer()
            self.availCount = streamio.BongoStreamGet(self.obj, self.buf)

        return self.availCount != 0

    def read(self, count=None):
        if count is None:
            # read to end
            out = StringIO.StringIO()

            bytes = self._read(self.bufLen)
            while bytes is not None:
                out.write(bytes)
                bytes = self._read(self.bufLen)

            return out.getvalue()
        else:
            return self._read(count)

    def _read(self, count):
        if not self._RefreshBuffer():
            return None

        if count > self.availCount:
            toRead = self.availCount
        else:
            toRead = count

        # copy the array
        ret = self.buf[self.availOffset:self.availOffset+self.availCount]

        self.availCount = self.availCount - toRead
        self.availOffset = self.availOffset + toRead
        return ret
