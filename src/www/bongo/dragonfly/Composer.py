import tempfile
from StringIO import StringIO

from bongo.external.email.utils import parseaddr, formataddr, fix_eols, formatdate
from bongo.external.email.message import Message
from bongo.external.email.header import Header, decode_header
from bongo.external.email.payloads import Payload, FilePayload, MemoryPayload, get_string_decoder
from bongo.external.email.parser import Parser
from bongo.external.email.feedparser import FeedParser
from bongo.external.email.generator import Generator

from libbongo.libs import msgapi

from bongo.store.CommandStream import CommandError
from bongo.store.StoreClient import DocTypes

class TemplatePayload(Payload) :
    def __init__(self, childPayload) :
        self.childPayload = childPayload
        self.encoding = None

    def set_template(self, template) :
        self.template = template

    def set(self, buffer) :
        self.childPayload.set(buffer)

    def add(self, chunk) :
        self.childPayload.add(chunk)

    def apply_template(self, string) :
        for key in self.template.keys() :
            string = string.replace("[" + key + "]", self.template[key])
        return string

    def strip_eol(self) :
        self.childPayload.strip_eol()

    def find_leftover(self, buf) :
        # imposes a max length on template words
        idx = buf.rfind('[', -20)
        if idx != -1 :
            if buf.rfind(']', idx) != -1 :
                return (buf, None)
            else :
                return (buf[:idx], buf[idx:])
        return (buf, None)

    def iter(self, decode) :
        if decode :
            decoder = get_string_decoder(self.encoding)
        else :
            decoder = None

        leftover = None
        for childBuf in self.childPayload.iter(True) :
            if leftover :
                childBuf = leftover + childBuf
                leftover = None

                (childBuf, leftover) = self.find_leftover(childBuf)

            if childBuf :
                childBuf = self.apply_template(childBuf)
                if decoder :
                    yield decoder(childBuf)
                else :
                    yield childBuf
        if leftover :
            childBuf = self.apply_template(leftover)
            if decoder :
                yield decoder(childBuf)
            else :
                yield childBuf

class ExistingFilePayload(FilePayload) :
    def __init__(self, file) :
        self.encoding = None
        self._f = file

class FixEolFile :
    """I don't understand why python's mail library outputs illegal mail"""
    def __init__(self, fp) :
        self.fp = fp
        self.waiting = None

    def __iter__(self) :
        return self

    def next(self) :
        return self.fp.next()

    def close(self) :
        return self.fp.close()

    def isatty(self) :
        return self.fp.isatty()

    def seek(self, pos, mode = 0) :
        return self.fp.seek(pos, mode)

    def tell(self) :
        return self.fp.tell()

    def read(self, n = -1) :
        return self.fp.read(n)

    def readline(self, length=None) :
        return self.fp.readline(length)

    def readlines(self, sizehint = 0) :
        return self.fp.readlines(sizehint)

    def truncate(self, size=None) :
        return self.fp.truncate(size)

    def write(self, s) :
        if len(s) > 0 and s[-1] == '\r' :
            # This may be a paired \r that has a matching \n in the next buffer, save it in case
            self.waiting = s[-1]
            s = s[:-1]

        if self.waiting :
            s = self.waiting + s
            self.waiting = None
            
        s = fix_eols(s)
        return self.fp.write(s)
    
    def writelines(self, iterable) :
        for line in iterable :
            self.write(line)

    def flush(self) :
        if self.waiting :
            self.fp.write("\r\n")

        return self.fp.flush()
    
class Composer :
    def __init__(self) :
        self.msg = Message()
        self.msg["Content-Type"] = "text/plain"
        self.msg["MIME-Version"] = "1.0"
        self.msg["User-Agent"] = "Bongo r" + str(msgapi.GetBuildVersion()[0]) + " (Dragonfly)"
        
        self.msg.set_payload('')

        self.bcc = []

    def LoadFromFile(self, f) :
        p = Parser()
        self.msg = p.parse(f)

    def WriteToFile(self, f) :
        fixFile = FixEolFile(f)
        g = Generator(fixFile)
        g.flatten(self.msg)        

    def _StringHeader(self, value) :
        return str(Header(unicode(value), 'ISO-8859-1'))
    
    def _AddressHeader(self, addresses) :
        if not addresses or len(addresses) == 0 :
            return None
        
        ret = ""
        for (name, address) in addresses :
            name = self._StringHeader(name)
            address = address.encode('ascii')

            if ret != "" :
                ret += ', ' 
            ret += formataddr((name, address))

        return ret

    def _SplitAddressLine(self, line) :            
        ret = []

        if not line :
            return ret

        addresses = line.strip("\r\n").split(",")
        for address in addresses :
            if address == '' or address is None :
                continue

            parsed = parseaddr(address)
            if parsed != ('', '') :
                ret.append(parsed)

        return ret

    def _SplitAddressProp(self, prop) :
        ret = []

        if not prop :
            return ret

        addresses = prop.strip("\r\n").split("\n")
        for address in addresses :
            if address == '' or address is None :
                continue

            parsed = parseaddr(address)
            if parsed != ('', '') :
                ret.append(parsed)

        return ret

    def _JoinAddressProp(self, props) :
        ret = []
        for (name, address) in props :
            if name is not None and name != "" :
                ret.append("%s <%s>" % (name, address))
            else :
                ret.append("<%s>" % (address))
        return "\n".join(ret)
        
    def SetDate(self, date) :
        del self.msg["Date"]
        self.msg["Date"] = formatdate(date, localtime=True)        

    def SetAddresses(self, header, addresses) :
        if header == "Bcc" :
            self.bcc = addresses
        else :
            del self.msg[header]
            if addresses != [] :
                self.msg[header] = self._AddressHeader(addresses)

    def SetAddressLine(self, header, line) :        
        if line :
            addresses = self._SplitAddressLine(line)
        else :
            addresses = []
        self.SetAddresses(header, addresses)        

    def GetAddresses(self, header) :
        if header == "Bcc":
            if self.bcc :
                return self.bcc
            else :
                return []
        else :
            value = self.msg[header]
            if value and value != "":
                decoded = decode_header(value)
                headerValue = ""
                for (text, charset) in decoded :
                    headerValue += text
                return self._SplitAddressLine(headerValue)
            else :
                return []

    def SetHeader(self, header, value) :
        del self.msg[header]
        self.msg[header] = self._StringHeader(value)

    def GetHeader(self, header) :
        return decode_header(self.msg[header])
        
    def SetSubject(self, subject) :
        self.SetHeader("Subject", subject)

    def GetSubject(self) :
        return self.GetHeader("Subject")    

    def _GetContentType(self, part) :
        try :
            value = part["Content-Type"]
            pieces = value.split(';', 2)
            return pieces[0]
        except KeyError :
            return "text/plain"

    def _GetChild(self, msg, mimeType, create=True) :
        try :
            i = 0
            while True :
                child = msg.get_payload(i)
                if self._GetContentType(child) == mimeType :
                    return child
                i += 1
        except IndexError :
            # Didn't find a message with this mime type
            if create :
                newMsg = Message()
                newMsg["Content-Type"] = mimeType
                newMsg["MIME-Version"] = "1.0"
                msg.attach(newMsg)
                return newMsg
        return None

    def _MakeMultipart(self, msg, subtype) :
        newMsg = Message()
        newMsg["Content-Type"] = self._GetContentType(msg)
        newMsg["MIME-Version"] = "1.0"
        newMsg.set_payload(msg._payload, msg._charset)
        
        del msg["Content-Type"]
        msg["Content-Type"] = "multipart/%s" % (subtype)

        msg.set_payload(None, None)
        msg.attach(newMsg)

    def _CollapseMultipart(self, msg) :
        child = msg.get_payload(0)
        del msg["Content-Type"]
        msg["Content-Type"] = child["Content-Type"]
        msg.set_payload(child._payload)

    def _FindBodyPart(self, msg, mimeType, create=False) :
        """Get a mime body part for this mime type. There will be only
        one body part for each mime type."""

        msgType = self._GetContentType(msg)
        
        # If the message is already this mime type, just return it
        if msgType == mimeType :
            return msg
        elif msgType == "multipart/alternative" :
            return self._GetChild(msg, mimeType, create)
        elif msgType == "multipart/mixed" :
            return self._FindBodyPart(msg.get_payload(0), mimeType, create)
        else :
            # Make this message a multipart/alternative
            if create :
                self._MakeMultipart(msg, "alternative")
                return self._GetChild(msg, mimeType, create)
            else :
                return None

    def GetBody(self, mimeType, create=False) :
        return self._FindBodyPart(self.msg, mimeType, create)

    def _ListBodyParts(self, msg, ret) :
        msgType = self._GetContentType(msg)
        if msgType == "multipart/mixed" :
            self._ListBodyParts(msg.get_payload(0), ret)
        elif msgType == "multipart/alternative" :
            try :
                i = 0
                while True :
                    child = msg.get_payload(i)
                    ret.append(child)
                i += 1
            except IndexError :
                # Done listing payloads
                pass
        else :
            ret.append(msg)

    def GetBodyParts(self) :
        ret = []
        self._ListBodyParts(self.msg, ret)
        return ret

    def TemplatizePart(self, part, dict) :
        template = None
        if isinstance(part._payload, TemplatePayload) :
            template = part._payload
        elif isinstance(part._payload, Payload) :
            template = TemplatePayload(part._payload)
            part._payload = template
        elif isinstance(part._payload, str) :
            child = MemoryPayload(None, part._payload)
            template = TemplatePayload(child)
            part._payload = template

        template.set_template(dict)

    def TemplatizeBody(self, dict) :
        bodyParts = self.GetBodyParts()
        for part in bodyParts :
            self.TemplatizePart(part, dict)

    def _BestCharset(self, value) :
        for body_charset in ('US-ASCII', 'ISO-8859-1', 'UTF-8'):
            try:
                value = value.encode(body_charset)
            except UnicodeError:
                pass
            else:
                break
        return (value, body_charset)
        
    def SetBody(self, payload, mimeType="text/plain", charset = None) :
        if isinstance(payload, str) or isinstance(payload, unicode):
            if not charset :
                (payload, charset) = self._BestCharset(payload)
            else :
                payload = payload.encode(charset)
            payload = MemoryPayload(None, payload)

        part = self._FindBodyPart(self.msg, mimeType, True)
        part.set_payload(payload, charset)
        
        return part

    def GetAttachmentPartByMimeType(self, mimeType) :
        return self._GetChild(self.msg, mimeType)

    def Attach(self, payload, filename, mimeType, charset = None) :
        if isinstance(payload, str) or isinstance(payload, unicode):
            if not charset :
                (payload, charset) = self._BestCharset(payload)
            else :
                payload = payload.encode(charset)
            payload = MemoryPayload(None, payload)

        if self._GetContentType(self.msg) != "multipart/mixed" :
            self._MakeMultipart(self.msg, "mixed")
        newMsg = Message()
        newMsg["Content-Type"] = mimeType
        newMsg["MIME-Version"] = "1.0"
        newMsg.set_payload(payload, charset)
        newMsg.add_header('Content-Disposition', 'attachment', filename=filename)
        
        self.msg.attach(newMsg)
        return newMsg
        
    def AttachMessage(self, msg) :
        if self._GetContentType(self.msg) != "multipart/mixed" :
            self._MakeMultipart(self.msg, "mixed")
        
        self.msg.attach(msg)

    def RemoveAttachment(self, filename) :
        if self._GetContentType(self.msg) != "multipart/mixed" :
            return

        parts = self.msg.get_payload_obj()
        for part in parts :
            if part.get_filename() == filename :
                parts.remove(part)
                if len(parts) < 2 :
                    # Not a multipart anymore!
                    self._CollapseMultipart(self.msg)
                return
    
    def AsString(self) :
        f = StringIO()
        self.WriteToFile(f)
        return f.getvalue()

class StoreComposer(Composer) :
    def __init__(self) :
        Composer.__init__(self)
        self.forceConversation = None

    def _StartRead(self, store, doc) :
        # Violate some rules
        store.stream.Write("READ %s" % (doc))
        r = store.stream.GetResponse()
        if r.code != 2001 :
            raise CommandError(r)
        length = r.message.split(" ")[-1]
        return int(length)
    
    def _ParseFromStore(self, store, doc) :
        parser = FeedParser(_payloadfactory=FilePayload)

        length = self._StartRead(store, doc)
        remaining = length
        while remaining > 0 :
            toRead = 4096
            if remaining < toRead :
                toRead = remaining
            data = store.stream.Read(toRead)
            remaining -= len(data)
            parser.feed(data)
        # eat the \r\n afterward
        store.stream.Read(2)

        msg = parser.close()
        
        return msg
    
    def ReadFromStore(self, store, doc, props=[]) :
        myprops = ["bongo.bcc"]
        myprops.extend(props)
        
        self.msg = self._ParseFromStore(store, doc)

        self.doc = store.Info(doc, props=myprops)
        self.bcc = self._SplitAddressProp(self.doc.props.get("bongo.bcc"))

    def WriteToStore(self, store, doc) :
        #print "saving\n%s" % (self.AsString())

        f = tempfile.TemporaryFile()
        self.WriteToFile(f)
        length = f.tell()
        f.seek(0)

        if not doc :
            if self.forceConversation :
                conv = " L%s" % (self.forceConversation)
            else :
                conv = ""
            store.stream.Write("WRITE /mail/drafts %d %d%s" % (DocTypes.Mail, length, conv))
        else :
            store.stream.Write("REPLACE %s %d" % (doc, length))

        r = store.stream.GetResponse()
        if r.code != 2002 :
            raise CommandError(r)

        remaining = length
        while remaining > 0 :
            toRead = 4096
            if toRead > remaining :
                toRead = remaining
            data = f.read(toRead)
            remaining -= len(data)
            store.stream.WriteRaw(data)

        f.close()

        r = store.stream.GetResponse()
        if r.code != 1000 :
            raise CommandError(r)

        if not doc :
            (doc, junk) = r.message.split(" ", 1)

        store.PropSet(doc, "bongo.bcc", self._JoinAddressProp(self.bcc))
            
        return doc
        
    def ReplyToDocument(self, store, uid) :
        doc = store.Info(uid, props=["nmap.mail.messageid", "bongo.references", "nmap.mail.conversation"])
        if doc :
            msgid = doc.props.get("nmap.mail.messageid")
            if msgid :
                self.msg["In-Reply-To"] = msgid
                references = doc.props.get("bongo.references")
                if references :
                    references = references + " " + msgid
                else :
                    references = msgid
                self.msg["References"] = references
            self.forceConversation = doc.props["nmap.mail.conversation"]
                
    def ForwardDocument(self, store, uid) :
        doc = store.Info(uid, props=["nmap.mail.conversation"])
        if doc :
            self.forceConversation = doc.props["nmap.mail.conversation"]
            
        attachMsg = self._ParseFromStore(store, uid)
        
        rfc822 = Message()
        rfc822["Content-Type"] = "message/rfc822"
        rfc822['Content-Disposition'] = "inline"
        rfc822['Content-Description'] = "Forwarded message: %s" % (attachMsg['Subject'])
        rfc822.set_payload([attachMsg])
        
        self.AttachMessage(rfc822)
    

    def _StoreToQueue(self, queue) :
        f = tempfile.TemporaryFile()
        self.WriteToFile(f)
        length = f.tell()
        f.seek(0)

        store.stream.Write("QSTOR MESSAGE %d" % (length))

        remaining = length
        while remaining > 0 :
            toRead = 4096
            if toRead > remaining :
                toRead = remaining
            data = f.read(toRead)
            remaining -= len(data)
            store.stream.WriteRaw(data)

        f.close()

        r = store.stream.GetResponse()
        if r.code != 1000 :
            raise CommandError(r)        

    def Send(self, queue) :
        try :
            queue.Create()
            for (name, address) in self.GetAddresses("From") :
                queue.StoreFrom(address)

            for (name, address) in self.GetAddresses("To") :
                queue.StoreTo(address, address)

            for (name, address) in self.GetAddresses("Cc") :
                queue.StoreTo(address, address)

            for (name, address) in self.GetAddresses("Bcc") :
                queue.StoreTo(address, address)

            queue.StoreMessage(self.AsString())
        
            queue.Run()
        except :
            queue.Abort()
            raise
        
