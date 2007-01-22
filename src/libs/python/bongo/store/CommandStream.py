import re
import logging
from bongo.BongoError import BongoError

__all__ = ["CommandError",
           "CommandStream",
           "Item",
           "ItemIterator",
           "MimeItem",
           "Response",
           "ResponseIterator"]

class Response:
    def __init__(self, code, message, data=None):
        self.code = int(code)
        self.message = message
        self.data = data

class CommandError(BongoError):
    def __init__(self, response):
        BongoError.__init__(self, response.message)
        self.code = response.code

class Item:
    def __init__(self, response):
        # split on unescaped spaces
        ret = re.split(r"(?<!\\) ", response.message)

        # ditch the escaping backslashes
        ret = [re.sub(r"\\ ", " ", x) for x in ret]

        self.uid, self.type, self.flags, self.imapuid, self.created, self.bodylen, self.filename = ret

        self.flags = int(self.flags)
        self.type = int(self.type)

        self.props = {}

class MimeItem:
    def __init__(self, response):
        self.end = False
        self.children = []
        self.parent = None
        self.multipart = False

        if response.code == 2003 or response.code == 2004:
            self.end = True
            return

        self.type, self.subtype, self.charset, self.encoding, self.name, self.headerStart, self.headerLen, self.start, self.length, self.partHeaderLen, self.lines = re.findall(r'\".*\"|[^ ]+', response.message)

        self.type = self.type.lower()
        self.subtype = self.subtype.lower()
        self.contenttype = ("%s/%s" % (self.type, self.subtype))

        if self.type == "multipart":
            self.multipart = True

        if self.charset == "-":
            self.charset = None

        if self.encoding == "-":
            self.encoding = None

        self.name = self.name.strip('"')

        self.headerStart = int(self.headerStart)
        self.headerLen = int(self.headerLen)
        self.start = int(self.start)
        self.length = int(self.length)
        self.partHeaderLen = int(self.partHeaderLen)
        self.lines = int(self.lines)

class CommandStream:
    log = logging.getLogger("Bongo.StoreConnection")

    def __init__(self, socket):
        self.stream = socket.makefile()

    def close(self):
        self.stream.close()

    def Read(self, length=-1):
        return self.stream.read(length)

    def Write(self, str, nolog=False):
        if not nolog:
            self.log.debug("SEND: %s", str)
        self.stream.write(str)
        self.stream.write("\r\n")
        self.stream.flush()

    def WriteRaw(self, str, nolog=False):
        if not nolog:
            self.log.debug("SEND: %s", str)
        self.stream.write(str)
        self.stream.flush()

    def GetResponse(self, nolog=False):
        s = self.readline()
        if not nolog:
            self.log.debug("READ: %s", s)
        return Response(s[0:4], s[5:])

    def readline(self):
        s = self.stream.readline()
        if s[-2:] == "\r\n":
            s = s[:-2]
        elif s[-1:] == "\n":
            s = s[:-1]
        return s

class ResponseIterator:
    def __init__(self, stream):
        self.stream = stream

        self.done = False
        self.count = None
        self.total = None

    def __iter__(self):
        return self

    def next(self):
        r = self.stream.GetResponse()
        if r.code == 1000:
            if re.match("^\d+$", r.message):
                self.total = int(r.message)
            self.done = True
        elif r.code > 3000:
            raise CommandError(r)

        if self.done:
            raise StopIteration()

        return r

    def finish(self):
        for r in self:
            pass

class ItemIterator(ResponseIterator):
    def __init__(self, stream, propCount=0):
        ResponseIterator.__init__(self, stream)
        self.propCount = propCount

    def next(self):
        item = Item(ResponseIterator.next(self))
        for i in range(self.propCount):
            r = self.stream.GetResponse()
            if r.code == 2001:
                (key, length) = r.message.split(" ", 2)
                data = self.stream.Read(int(length))
                try:
                    item.props[key] = unicode(data, "utf-8")
                except UnicodeDecodeError, e:
                    self.stream.log.error("Bad utf-8 property from store: '%s'", data)
                    item.props[key] = data
                    
                # eat the \r\n afterward
                self.stream.Read(2)

        return item
