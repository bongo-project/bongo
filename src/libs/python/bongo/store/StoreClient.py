import logging
from CommandStream import *
from StoreConnection import StoreConnection
from cStringIO import StringIO
import bongo
import re
import time
import random
import md5
import socket
import string

class DocTypes:
    Unknown = 0x0001
    Mail = 0x0002
    Event = 0x0003
    Addressbook = 0x0004
    Conversation = 0x0005
    Calendar = 0x0006
    Config = 0x0007

    Folder = 0x1000
    Shared = 0x2000
    SharedFolder = Folder | Shared
    Purged = 0x4000

class DocFlags:
    Purged = (1 << 0)
    Seen = (1 << 1)
    Answered = (1 << 2)
    Flagged = (1 << 3)
    Deleted = (1 << 4)
    Draft = (1 << 5)
    Recent = (1 << 6)
    Junk = (1 << 7)
    Sent = (1 << 8)

    Starred = (3 << 16)

    names = {
        Purged : "purged",
        Seen : "read",
        Answered : "answered",
        Flagged : "flagged",
        Deleted : "deleted",
        Draft : "draft",
        Recent : "recent",
        Junk : "junk",
        Sent : "sent",
        Starred : "starred"
        }

    # reverse the names hash so we can look up flags by name
    revNames = {}
    for name, flag in names.items():
        revNames[flag] = name

    # some helpful functions
    def GetName(flag):
        return DocFlags.names[flag]
    GetName = staticmethod(GetName)

    def ByName(name):
        return DocFlags.revNames[name]
    ByName = staticmethod(ByName)

    def AllFlags():
        return DocFlags.names.keys()
    AllFlags = staticmethod(AllFlags)

    def GetMask(hash):
        flags = mask = 0

        for flag, status in hash.items():
            if status:
                flags = flags | flag
            mask = mask | flag

        return flags, mask
    GetMask = staticmethod(GetMask)

class FlagMode:
    Show = 0
    Add = 1
    Remove = 2
    Replace = 3

class ACL :
    def __init__(self, acl = None) :
        self.entries = []

        if acl :
            split = acl.split(';')
            for entry in split :
                if entry == '' :
                    continue
                
                tmp = re.split('\s+', entry)
                self.entries.append((tmp[0], tmp[1], tmp[2]))
        self.changed = False

    def __str__(self) :
        acl = ''
        for entry in self.entries :
            acl = acl + "%s %s %s;" % entry
        return acl

    def Clear(self) :
        if (self.entries != []) :
            self.entries = []
            self.changed = True

    def Grant(self, principal, priv) :
        newEntry = ("grant", principal, priv)
        for entry in self.entries :
            if entry == newEntry :
                return

        self.changed = True
        self.entries.append(newEntry)

    def AddEntries(self, entries) :
        self.changed = True
        self.entries.extend(entries)

    def Revoke(self, principal, priv) :
        self.RevokeEntry(("grant", principal, priv))

    def RevokeEntry(self, revokeEntry) :
        newEntries = []
        for entry in self.entries :
            if entry != revokeEntry :
                newEntries.append(entry)
            else :
                self.changed = True

        self.entries = newEntries

    def RevokeEntries(self, entries) :
        for entry in entries :
            self.RevokeEntry(entry)

class CalendarACL :
    class Rights :
        Read = (1 << 0)
        Write = (1 << 1)
        ReadProps = (1 << 2)

    def __init__(self, acl) :
        self.acl = acl
        self.summary = None

    def __str__(self) :
        return str(self.acl)

    def GetACL(self) :
        return self.acl

    def _GetUid (self, *args):
        t = long(time.time() * 1000)
        r = long(random.random() * 100000000000000000L)
        try:
            a = socket.gethostbyname(socket.gethostname())
        except:
            # if we can't get a network address, just imagine one
            a = random.random() * 100000000000000000L
        data = str(t) + ' ' + str (r) + ' ' + str(a) + ' ' + str(args)
        data = md5.md5(data).hexdigest()
        return data

    
    def _GetRights(self, name) :
        rights = 0
        if name == "read" :
            rights = rights | CalendarACL.Rights.Read
        elif name == "write" :
            rights = rights | CalendarACL.Rights.Write
        elif name == "read-props" :
            rights = rights | CalendarACL.Rights.ReadProps
        return rights

    def RightsToString(self, rights) :
        names = []
        if rights == 0 :
            return "none"
        if rights & CalendarACL.Rights.Read :
            names.append("read")
        if rights & CalendarACL.Rights.Write :
            names.append("write")
        if rights & CalendarACL.Rights.ReadProps :
            names.append("read-props")
        return ", ".join(names)


    def SetPublic(self, rights) :
        self.summary = None
        entries = self._Find("all")
        if (entries) :
            self.acl.RevokeEntries(entries)
        if (rights & CalendarACL.Rights.Read) :
            self.acl.Grant("all", "read")
        if (rights & CalendarACL.Rights.Write) :
            self.acl.Grant("all", "write")
        if (rights & CalendarACL.Rights.ReadProps) :
            self.acl.Grant("all", "read-props")

    def GetPublic(self) :
        entries = self._Find("all")
        rights = 0
        for entry in entries :
            rights = rights | self._GetRights(entry[2])
        return rights

    def AddPassword(self, user, password, rights) :
        self.summary = None
        if (rights & CalendarACL.Rights.Read) :
            self.acl.Grant("token:pw:" + user + ":" + password, "read")
        if (rights & CalendarACL.Rights.Write) :
            self.acl.Grant("token:pw:" + user + ":" + password, "write")

    def CheckPassword(self, user, password) :
        entries = self._Find("token:pw:" + user + ":" + password)
        if entries :
            return True
        else :
            return False

    def HasPassword(self, user = None) :
        entries = self._FindPasswords()
        if entries :
            return True
        else :
            return False

    def RemovePassword(self, user, password) :
        self.summary = None
        self.acl.RemoveEntries(self._Find("token:pw:" + user + ":" + password))

    def RemovePasswords(self) :
        self.summary = None
        self.acl.RemoveEntries(self._FindPasswords())

    def GetPasswords(self) :
        entries = self._FindPasswords()
        ret = {}
        for entry in entries :
            split = entry[1].split(":")
            ret[split[2]] = split[3]

        return ret

    def Share(self, address, rights, password = None) :
        self.summary = None
        entries = self._FindAddress(address)

        if (entries) :
            if not password :
                token = entries[0][1]
            else :
                token = "token:email:%s:%s" % (address, password)
                
            self.acl.RevokeEntries(entries)
        else :
            if not password :
                password = self._GetUid()
            token = "token:email:%s:%s" % (address, password)

        if (rights & CalendarACL.Rights.Read) :
            self.acl.Grant(token, "read")
        if (rights & CalendarACL.Rights.Write) :
            self.acl.Grant(token, "write")
        if (rights & CalendarACL.Rights.ReadProps) :
            self.acl.Grant(token, "read-props")

        return token[len("token:"):]

    def Unshare(self, address) :
        entries = self._FindAddress(address)
        if (entries) :
            self.acl.RevokeEntries(entries)

    def GetAddressToken(self, address) :
        entries = self._FindAddress(address)
        token = entries[0][1]
        return token[len("token:"):]
        
    def UnshareAll(self) :
        rights = self.GetPublic()
        self.acl.Clear()
        self.SetPublic(rights)

    def Clear(self) :
        self.acl.Clear()

    def Summarize(self) :
        if self.summary :
            return self.summary

        public = 0
        addresses = {}
        passwords = {}

        for entry in self.acl.entries :
            principal = entry[1]
            rights = self._GetRights(entry[2])

            if principal == "all" :
                public = public | rights
            elif principal.startswith("token:email:") :
                remaining = principal[len("token:email:"):]
                split = remaining.split(':', 2)
                address = split[0]
                password = split[1]

                if addresses.has_key(address) :
                    entry = addresses[address]
                else :
                    entry = { "password": password, "rights": 0 }
                    addresses[address] = entry
                entry["token"] = principal[len("token:"):]
                entry["rights"] = entry["rights"] | rights
            elif principal.startswith("token:") :
                password = principal[len("token:"):]
                if (passwords.has_key(password)) :
                    passwords[password] = passwords[password] | rights
                else :
                    passwords[password] = rights

        ret = {}
        ret["passwords"] = passwords
        ret["addresses"] = addresses
        ret["public"] = public
        self.summary = ret
        return ret
    
    def _FindAddress(self, address) :
        """Find acls with a given email token prefix"""
        ret = []
        for entry in self.acl.entries :
            if entry[1].startswith ("token:email:" + address + ":") :
                ret.append(entry)
        return ret

    def _Find(self, principal) :
        """Find acls with a given principal"""
        ret = []
        for entry in self.acl.entries :
            if entry[1] == principal :
                ret.append(entry)
        return ret

    def _FindPasswords(self) :
        """Find all password acls"""
        ret = []
        for entry in self.acl.entries :
            if entry[1].startswith("token:pw:") :
                ret.append(entry)
        return ret

class StoreClient:
    def __init__(self, user, owner, authToken=None, authCookie=None,
                 authPassword=None, host="localhost", port=689):
        #from libbongo.libs import msgapi
        self.owner = self.user = None
        
        #addr = msgapi.FindUserNmap(owner)
        #if addr is None:
        #    raise bongo.BongoError ("Could not look up nmap host for user %s" % owner)
        #(host, port) = addr
        
        if authCookie:
            self.connection = StoreConnection(host, port, systemAuth=False)
            if not self.connection.CookieAuth(user, authCookie):
                raise bongo.BongoError("Failed cookie authentication with store")
        elif authPassword:
            self.connection = StoreConnection(host, port, systemAuth=False)
            if not self.connection.UserAuth(user, authPassword):
                raise bongo.BongoError("Failed user authentication with store")
        else:
            self.connection = StoreConnection(host, port)

        self.stream = self.connection.stream

        # if we authed with a cookie or password, we don't need to USER
        if not authCookie and not authPassword and user is not None:
            self.User(user)
        self.Store(owner)

        if authToken is not None:
            self.Token(authToken)

    def _escape_arg(self, arg):
        return "\"" + self._escape_quotes(arg) + "\""

    def _escape_quotes(self, s):
        return s.replace('"', '\\"')

    def _escape_query(self, s):
        return self._escape_quotes(s)

    def Collections(self, path=""):
        command = "COLLECTIONS %s" % path
        self.stream.Write(command)
        return CollectionIterator(self.stream)

    def CookieDelete(self, token):
        command = "COOKIE DELETE %s" % token

        stream.log.debug("SEND: COOKIE DELETE ****************")
        self.stream.Write(command, nolog=1)

        r = self.stream.GetResponse()

        if r.code != 1000:
            raise CommandError(r)

    def CookieNew(self, timeout):
        command = "COOKIE NEW %d" % (time.time() + timeout)
        self.stream.Write(command)

        r = self.stream.GetResponse(nolog=True)

        if r.code != 1000:
            self.stream.log.debug("READ: %s %s", r.code, r.message)
            raise CommandError(r)

        self.stream.log.debug("READ: 1000 ****************")
        return r.message

    def Create(self, path, guid="", existingOk=False):
        command = "CREATE %s %s" % (path, guid)
        self.stream.Write(command)

        r = self.stream.GetResponse()

        if r.code != 1000:
            if r.code != 4226 or not existingOk:
                raise CommandError(r)

        return r

    def Conversation(self, uid, props=[]):
        command = "CONVERSATION %s" % uid

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        self.stream.Write(command)
        return ItemIterator(self.stream, len(props))

    def Conversations(self, source=None, props=[], query=None, start=-1, end=-1,
                      center=None, showTotal=False, flags=None, mask=None):
        command = "CONVERSATIONS"

        if source is not None:
            command = command + " S" + source

        if start == -1 and end == -1:
            command = command + " Rall"
        else:
            command = command + " R%d-%d" % (start, end)

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        if query is not None:
            command = command + " \"Q" + self._escape_query(query) + "\""

        if center is not None:
            command = command + " C" + center

        if flags is not None:
            command = command + " F" + str(flags)

        if mask is not None:
            command = command + " M" + str(mask)

        if showTotal:
            command = command + " T"

        self.stream.Write(command)
        return ItemIterator(self.stream, len(props))

    def Delete(self, doc):
        self.stream.Write("DELETE %s" % (doc))
        r = self.stream.GetResponse()

        if r.code != 1000:
            raise CommandError(r)

    def Events(self, calendar=None, props=[], query=None, start=None, end=None):
        command = "EVENTS"

        if calendar is not None:
            command = command + " C" + calendar

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        if query is not None:
            command = command + " \"Q" + self._escape_query(query) + "\""

        if start != None and end != None:
            command = command + " \"D%s-%s\"" % (start.strftime('%Y%m%dT%H%M%S'), end.strftime('%Y%m%dT%H%M%S'))

        self.stream.Write(command)
        return ItemIterator(self.stream, len(props))

    def Flag(self, guid, flags=None, mode=None):
        command = "FLAG %s" % guid

        if flags is None:
            mode = FlagMode.Show
        elif mode is None:
            mode = FlagMode.Replace
            command = "%s %d" % (command, flags)
        elif mode == FlagMode.Add:
            command = "%s +%d" % (command, flags)
        elif mode == FlagMode.Remove:
            command = "%s -%d" % (command, flags)

        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

        (old, new) = r.message.split(" ", 1)
        return (old, new)

    def GetACL(self, doc) :
        command = "PROPGET \"%s\" nmap.access-control" % doc
        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 2001 and r.code != 3245:
            raise CommandError(r)

        acl = ""
        if r.code == 2001 :
            (key, length) = r.message.split(" ", 2)
            acl = self.stream.Read(int(length))
            # eat the \r\n afterward
            self.stream.Read(2)

        return ACL(acl)

    def Info(self, path, props=[]):
        command = "INFO %s" % self._escape_arg(path)

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        self.stream.Write(command)
        r = self.stream.GetResponse()

        if r.code == 2001:
            item = Item(r)

            for i in range(len(props)):
                r = self.stream.GetResponse()
                if r.code == 2001:
                    (key, length) = r.message.split(" ", 2)
                    item.props[key] = self.stream.Read(int(length))
                    # eat the \r\n afterward
                    self.stream.Read(2)

            # eat the response line
            self.stream.GetResponse()
            return item
        elif r.code == 1000:
            return None

        raise CommandError(r)

    def ISearch(self, query, props = [], start=-1, end=-1):
        command = "ISEARCH "

        if start == -1 and end == -1:
            command = command + " Rall"
        else:
            command = command + " R%d-%d" % (start, end)

        command = "%s \"%s\"" % (command, self._escape_query(query))

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        self.stream.Write(command)
        return ItemIterator(self.stream, len(props))

    def Link(self, calendar, doc) :
        self.stream.Write("LINK \"%s\" %s" % (calendar, doc))
        r = self.stream.GetResponse()
        if r.code != 1000 :
            raise CommandError(r)

    def List(self, path, props=[], start=-1, end=-1, flags=None, mask=None):
        path = path.replace(" ", "\ ")
        command = "LIST %s" % path

        if start == -1 and end == -1:
            command = command + " Rall"
        else:
            command = command + " R%d-%d" % (start, end)

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        if flags is not None:
            command = command + " F" + str(flags)

        if mask is not None:
            command = command + " M" + str(mask)

        self.stream.Write(command)
        return ItemIterator(self.stream, len(props))

    def MailingLists(self, source):
        command = "MAILINGLISTS %s" % source
        self.stream.Write(command)
        return ResponseIterator(self.stream)

    def Mime(self, uid):
        command = "MIME %s" % uid

        self.stream.Write(command)

        parts = []

        r = self.stream.GetResponse()
        while r.code in (2002, 2003, 2004):
            parts.append(MimeItem(r))
            r = self.stream.GetResponse()

        if len(parts) == 0:
            return None

        topPart = parts[0]

        if topPart.multipart:
            parent = topPart

            for child in parts[1:]:
                child.parent = parent

                if child.end:
                    parent = parent.parent
                    if parent is None:
                        break
                else:
                    parent.children.append(child)
                    if child.multipart:
                        parent = child

        if r.code != 1000:
            raise CommandError(r)

        return topPart

    def Move(self, uid, path, filename=None):
        command = "MOVE %s \"%s\"" % (uid, path)

        if filename is not None:
            command = "%s \"%s\"" % (command, filename)
        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

    # this function is here to coerce strings in unknown encodings
    # into something approaching utf-8. This is obviously lossy,
    # and we really don't want to have to do this - need to get the
    # store as utf-8 clean as possible, really.
    def force_utf8(self, str):
        out = []
        append = out.append
        for ch in str:
            if ch < "\200":
                append(ch)
            else:
                append('?')
        return string.join(out, "")

    def PropGet(self, doc, name=None):
        command = "PROPGET %s" % doc

        if name is not None:
            self.stream.Write("%s %s" % (command, name))
            r = self.stream.GetResponse()
            if r.code != 2001:
                raise CommandError(r)
            (key, length) = r.message.split(" ", 2)
            raw_data = self.stream.Read(int(length))
            try:
                data = unicode(raw_data, "utf-8")
            except UnicodeDecodeError, e:
                data = self.force_utf8(raw_data)
            # eat the \r\n afterward
            self.stream.Read(2)
            return data
        
        self.stream.Write(command)
        props = {}
        r = self.stream.GetResponse()
        while r.code == 2001 or r.code == 3245:
            if r.code == 3245:
                # set empty properties to None
                props[key] = None
                r = self.stream.GetResponse()
                continue

            (key, length) = r.message.split(" ", 2)
            raw_data = self.stream.Read(int(length))
            try:
                props[key] = unicode(raw_data, "utf-8")
            except UnicodeDecodeError, e:
                data = self.force_utf8(raw_data)
            # eat the \r\n afterward
            self.stream.Read(2)
            r = self.stream.GetResponse()
        
        # automatically pull out imap_uid from Bongo 0.3.
        if name is None and not props.has_key("nmap.mail.imapuid"):
            self.stream.Write("PROPGET %s nmap.mail.imapuid" % (doc))
            r = self.stream.GetResponse()
            if r.code != 2001:
                raise CommandError(r)
            (key, length) = r.message.split(" ", 2)
            raw_data = self.stream.Read(int(length))
            self.stream.Read(2)
            props["nmap.mail.imapuid"] = raw_data
        return props

    def PropSet(self, doc, name, value=None):
        if value is None:
            value = ""

        self.stream.Write("PROPSET %s %s %d" % (doc, name, len(value)))

        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)

        self.stream.WriteRaw(value)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

    def Quit(self):
        self.stream.Write("QUIT")
        self.connection.Close()

    def Read(self, doc):
        doc = doc.replace(" ", "\ ")
        self.stream.Write("READ %s" % (doc))
        r = self.stream.GetResponse()
        if r.code != 2001:
            raise CommandError(r)
        length = r.message.split(" ")[-1]
        s = self.stream.Read(int(length))
        # eat the \r\n afterward
        self.stream.Read(2)
        return s

    def ReadMimePart(self, doc, part):
        self.stream.Write("READ %s %d %d" % (doc, part.start, part.length))
        r = self.stream.GetResponse()
        if r.code != 2001:
            raise CommandError(r)
        length = r.message.split(" ")[-1]
        s = self.stream.Read(int(length))
        # eat the \r\n afterward
        self.stream.Read(2)
        return StringIO(s)

    def Remove(self, collection):
        self.stream.Write("REMOVE %s" % collection)

        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
    
    def Rename(self, oldname, newname):
        self.stream.Write("RENAME %s %s" % (oldname, newname))
        
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

    def Repair(self):
        self.stream.Write("REPAIR")
        
        r = self.stream.GetResponse()
        while r.code in (2001, 2011, 2012):
            # TODO : want some kind of iterator in here really
            r = self.stream.GetResponse()
        
        if r.code != 1000:
            raise CommandError(r)

    def Replace(self, doc, data):
        self.stream.Write("REPLACE %s %d" % (doc, len(data)))

        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)

        self.stream.WriteRaw(data)

        r = self.stream.GetResponse()
        (guid, junk) = r.message.split(" ", 1)
        return guid

    def Reset(self):
        self.stream.Write("RESET")
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

    def SetACL(self, doc, acl, force=False) :
        if force or acl.changed :
            self.PropSet(doc, "nmap.access-control", str(acl))
        
    def Store(self, name):
        self.stream.Write("STORE %s" % (name))
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        self.owner = name
        return r

    def Token(self, token):
        self.stream.Write("TOKEN %s" % (token))
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        return r

    def Unlink(self, calendar, event):
        self.stream.Write("UNLINK %s %s" % (calendar, event))
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        return r

    def User(self, name):
        self.stream.Write("USER %s" % (name))
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        self.user = name
        return r

    def WriteImap(self, collection, type, imap, num, filename=None, index=None, guid=None, timeCreated=None, flags=None, link=None):

        typ, data = imap.fetch(num, '(RFC822.SIZE)')
        cnt = int(re.findall(r'RFC822\.SIZE (\d+)', data[0])[0])

        command = "WRITE \"%s\" %d %d" % (collection, type, cnt)

        if index is not None:
            command = command + " I%s" % index

        if filename is not None:
            command = command + " \"F%s\"" % filename

        if guid is not None:
            command = command + " G%s" % guid

        if timeCreated is not None:
            command += " T" + timeCreated

        if flags is not None:
            command = command + " Z%d" % flags

        if link is not None:
            command = command + " \"L%s\"" % link

        self.stream.Write(command)
        

        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)

        self.stream.WriteRaw(imap.fetch(num, '(body[])')[1][0][1])

        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

        (guid, junk) = r.message.split(" ", 1)
        return guid

    def Write(self, collection, type, data, filename=None, index=None, guid=None, timeCreated=None, flags=None, link=None, noProcess=None):
        if data is None:
            data = ""

        command = "WRITE \"%s\" %d %d" % (collection, type, len(data))

        if index is not None:
            command = command + " I%s" % index

        if filename is not None:
            command = command + " \"F%s\"" % filename

        if guid is not None:
            command = command + " G%s" % guid

        if timeCreated is not None:
            command += " T" + timeCreated

        if flags is not None:
            command = command + " Z%d" % flags

        if link is not None:
            command = command + " \"L%s\"" % link
        
        if noProcess is not None:
            command = command + " N"

        self.stream.Write(command)
        

        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)

        self.stream.WriteRaw(data)

        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

        (guid, junk) = r.message.split(" ", 1)
        return guid

    def Reset(self) :
        self.stream.Write("RESET\r\n")
        self.stream.GetResponse()
        

