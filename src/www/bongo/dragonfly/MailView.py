from bongo.store.QueueClient import QueueClient
from bongo.store.StoreClient import StoreClient, DocFlags, DocTypes, FlagMode, CalendarACL, ACL
from bongo.StreamIO import Stream
from bongo.dragonfly.ResourceHandler import ResourceHandler
from bongo.dragonfly.HttpError import HttpError
from bongo.store.CommandStream import CommandError

import bongo
import bongo.dragonfly
from bongo.libs import msgapi

from Composer import StoreComposer, ExistingFilePayload

from AddressbookView import ContactsHandler as AddressbookContactsHandler

from bongo.external.email.utils import parseaddr, formatdate

import cStringIO as StringIO
import re
import time
import math
import urllib
import traceback

class ConversationsHandler(ResourceHandler):
    pageSize = 30

    def _UpdateComposer(self, composer, jsob) :
        composer.SetAddressLine("From", jsob.get("from"))
        composer.SetAddressLine("To", jsob.get("to"))
        composer.SetAddressLine("Cc", jsob.get("cc"))
        composer.SetAddressLine("Bcc", jsob.get("bcc"))
        composer.SetSubject(jsob.get("subject"))

        if jsob.has_key("body") :
            composer.SetBody(jsob.get("body"))
        if jsob.has_key("bodyHtml") :
            composer.SetBody(jsob.get("bodyHtml"), "text/html")

        composer.SetDate(time.time())

    def _CreateDraft(self, store, req, jsob):
        params = jsob["params"]

        message = params[0]
        inReplyTo = params[1]
        forward = params[2]
        invitationFor = params[3]
        useAuthUrls = params[4]

        composer = StoreComposer()
        self._UpdateComposer(composer, message)
        msgid = "<%s@%s>" % (self._GetUid(), self._ServerHostname(req))
        composer.SetHeader("Message-ID", msgid)
        if inReplyTo :
            composer.ReplyToDocument(store, inReplyTo)
        if forward :
            composer.ForwardDocument(store, forward)
        uid = composer.WriteToStore(store, None)
    
        store.Flag(uid, DocFlags.Draft | DocFlags.Seen)
        doc = store.Info(uid, props=["nmap.mail.conversation"])
        if useAuthUrls :
            store.PropSet(uid, "bongo.draft.use-auth-urls", "1")
        if invitationFor :
            store.PropSet(uid, "bongo.draft.invitation-for", invitationFor)
            
        ret = { "id": jsob.get("id"),
                "error": None,
                "result": { "bongoId" : uid, "conversation" : doc.props["nmap.mail.conversation"] } }
        return self._SendJson(req, ret)

    def _UpdateDraft(self, store, req, jsob, uid=None, silent=False):
        params = jsob["params"]

        message = params[0]

        composer = StoreComposer()
        if (uid) :
            composer.ReadFromStore(store, uid)
        self._UpdateComposer(composer, message)

        uid = composer.WriteToStore(store, uid)
        store.Flag(uid, DocFlags.Draft | DocFlags.Seen)
        doc = store.Info(uid, props=["nmap.mail.conversation"])
        
        if not silent:
            ret = { "id": jsob.get("id"),
                    "error": None,
                    "result": { "bongoId" : uid, "conversation" :  doc.props["nmap.mail.conversation"]} }
            return self._SendJson(req, ret)

        return bongo.dragonfly.OK


    def _SplitRecipients(self, composer) :
        ret = []
        for header in ("To", "Cc", "Bcc") :
            for (name, address) in composer.GetAddresses(header) :
                ret.append({ "header": header,
                             "recipient" : (name, address) })
        return ret
        
    def _CreateInvitation(self, req, owner, cal, token=None) :
        invitation = {}
        invitation["server"] = req.server.server_hostname
        invitation["calendarName"] = cal.filename
        invitation["ics"] = "[ICSURL]"

        return self._ObjToJson(invitation)

    def _InvitationTemplate(self, req, owner, cal, token=None) :
        template = {}
        baseurl = self._BaseUrl(req) + "/" + urllib.quote(owner) + "/calendar/" + urllib.quote(cal.filename) + "/events"
        icsurl = baseurl + ".ics"
        htmlurl = baseurl + ".html"

        if token :
            icsurl += "," + token
            htmlurl += "," + token
        
        template["ICSURL"] = icsurl
        template["HTMLURL"] = htmlurl

        return template

    def _DeliverToSingle(self, req, queue, composer, invitationPart, invitationCal) :
        if invitationCal :
            invitationTemplate = self._InvitationTemplate(req, req.user, invitationCal)
            composer.TemplatizeBody(invitationTemplate)
            composer.TemplatizePart(invitationPart, invitationTemplate)
        composer.Send(queue)

    def _DeliverToMultiple(self, req, queue, composer, invitationPart, invitationCal, invitationAcl, recipients) :
        for recip in recipients :
            for header in ("To", "Cc", "Bcc") :
                if header == recip["header"] :
                    composer.SetAddresses(header, [recip["recipient"]])
                    msgid = "<%s@%s>" % (self._GetUid(), self._ServerHostname(req))
                    composer.SetHeader("Message-ID", msgid)
                    if invitationCal:
                        invitationTemplate = self._InvitationTemplate(req,
                                                                      req.user,
                                                                      invitationCal,
                                                                      invitationAcl.GetAddressToken(recip["recipient"][1]))
                        composer.TemplatizeBody(invitationTemplate)
                        composer.TemplatizePart(invitationPart, invitationTemplate)
                else :
                    composer.SetAddresses(header, [])

            composer.Send(queue)

    def _DeliverMessage(self, store, req, jsob, rp):        
        msgid = rp.handlerData["message"]

        composer = StoreComposer()
        composer.ReadFromStore(store, msgid, props=["bongo.draft.use-auth-urls",
                                                    "bongo.draft.invitation-for",
                                                    "nmap.mail.conversation"])

        # use-auth-urls: Send one copy of the message to each
        # recipient, with invitation urls expanded to include
        # authentication tokens
        if composer.doc.props.get("bongo.draft.use-auth-urls") == "1" :
            recipients = self._SplitRecipients(composer)
        else :
            recipients = None

        # Invitation for a calendar
        invitationFor = composer.doc.props.get("bongo.draft.invitation-for")
        if invitationFor :
            invitationCal = store.Info(invitationFor, ["nmap.access-control"])
            invitation = self._CreateInvitation(req, req.user, invitationCal)
            invitationPart = composer.Attach(invitation, "Bongo Invitation", "text/x-bongo-invitation")
        else :
            invitationCal = None
            invitationPart = None            

        composer.SetHeader("Received",
                           "by %s with HTTP (Dragonfly); %s\r\n" % (self._ServerHostname(req), formatdate ()))

        queue = QueueClient(rp.user)

        try :
            if recipients:
                invitationAcl = CalendarACL(ACL(invitationCal.props["nmap.access-control"]))
                self._DeliverToMultiple(req, queue, composer, invitationPart, invitationCal, invitationAcl, recipients)
            else:
                self._DeliverToSingle(req, queue, composer, invitationPart, invitationCal)
        except Exception, e:
            req.log.error(e)
            print traceback.print_exc()
            ret = { "id": jsob.get("id"),
                    "error": str(e),
                    "result": None }
            queue.Quit()
            return self._SendJson(req, ret)

        store.Flag(msgid, DocFlags.Sent, FlagMode.Add)
        store.Flag(msgid, DocFlags.Draft, FlagMode.Remove)

        ret = { "id": jsob.get("id"),
                "error": None,
                "result": { "bongoId": msgid, "conversation": composer.doc.props["nmap.mail.conversation"]} }
        return self._SendJson(req, ret)

    def _SplitAddressProp(self, prop) :
        ret = []

        if not prop :
            return ret

        addresses = prop.strip("\r\n").split("\n")
        for address in addresses :
            if address == '' or address is None :
                continue
            ret.append(parseaddr(address))

        return ret

    def FormatAddresses(self, mfrom):
        addresses = self._SplitAddressProp(mfrom)
        ret = []
        for (name, address) in addresses:
            if name is not None and name != "":
                if len(addresses) == 1:
                    # if there's only one item in the list, use the whole thing
                    ret.append(name)
                else:
                    # grab the first name
                    ret.append(name.split(" ", 1)[0])
            else:
                ret.append(address)
        return ret

    def _JoinAddresses(self, addresses) :
        ret = []
        for (name, address) in addresses :
            if name is not None and name != "" :
                ret.append("%s <%s>" % (name, address))
            else :
                ret.append("<%s>" % (address))
        return ret

    def _ConversationsToJson(self, convs):
        ret = {}

        conversations = []

        for item in convs:
            jsob = self._PropsToJson(item.props)
            jsob["bongoId"] = item.uid
            jsob["flags"] = self.DocumentFlags(item.flags)

            conversations.append(jsob)

        ret["conversations"] = conversations

        return ret

    # Here are some substitutions for character sets found in the wild
    # that aren't in Python.
    charset_fixes = {
        "unicode-1-1-utf-7" : "utf-7"
        }

    def _PropsToJson(self, props):
        jsob = {}
        jsob["date"] = props["nmap.conversation.date"];

        mfrom = props.get("bongo.from")

        if mfrom is None:
            jsob["from"] = []
            jsob["rawfrom"] = []
        else:
            jsob["from"] = self.FormatAddresses(mfrom)
            jsob["rawfrom"] = self._JoinAddresses(self._SplitAddressProp(mfrom))

        jsob["subject"] = props.get("bongo.conversation.subject", props.get("nmap.conversation.subject", "")) 
        jsob["unread"] = int(props.get("nmap.conversation.unread", 1))
        jsob["count"] = int(props.get("nmap.conversation.count", 1))
        jsob["snippet"] = "there is no snippet.";
        return jsob

    def _MsgToJson(self, store, msg, props):
        obj = {}
        obj = {"bongoId" : msg.uid}
        obj["from"] = self._JoinAddresses(self._SplitAddressProp(props.get("bongo.from", "")))
        obj["to"] = self._JoinAddresses(self._SplitAddressProp(props.get("bongo.to", "")))
        obj["cc"] = self._JoinAddresses(self._SplitAddressProp(props.get("bongo.cc", "")))
        obj["bcc"] = self._JoinAddresses(self._SplitAddressProp(props.get("bongo.bcc", "")))

        obj["date"] = props.get("nmap.mail.sent", "")
        obj["subject"] = props.get("nmap.mail.subject", "")

        obj["flags"] = self.DocumentFlags(msg.flags)
        obj["attachments"] = ""
        obj["snippet"] = "there is no snippet."

        obj["contents"] = self._MessageToJson(store, msg)
        return obj

    def _BuildDecoder(self, stream, part, display):
        decoder = Stream(stream)

        if part.encoding is not None:
            decoder.AddCodec(part.encoding)

        if display and part.contenttype != "text/plain":
            decoder.AddCodec(part.contenttype)

        return decoder

    def _GenerateContent(self, store, uid, part):
        if part.contenttype == "text/html" \
               or part.contenttype == "text/plain":
            stream = store.ReadMimePart(uid, part)
            decoder = self._BuildDecoder(stream, part, False)
            return decoder.read()

    def _GeneratePreview(self, store, uid, part):
        if part.contenttype == "text/html" \
               or part.contenttype == "text/plain":
            stream = store.ReadMimePart(uid, part)
            decoder = self._BuildDecoder(stream, part, True)
            return (part.contenttype, decoder.read())
        elif part.contenttype == "message/rfc822":
            stream = store.ReadMimePart(uid, part)
            decoder = self._BuildDecoder(stream, part, True)
            return ("text/plain", decoder.read())

    def _ChooseAlternative(self, part):
        best = None

        for child in part.children:
            if child.contenttype == "text/html":
                return child
            elif child.contenttype == "text/plain":
                best = child

        return best

    def _PartToJson(self, store, uid, part, includePreview=True, includeContent=False):
        jsob = {}
        jsob["contenttype"] = part.contenttype

        if part.name is not None:
            jsob["name"] = part.name

        if includePreview:
            preview = self._GeneratePreview(store, uid, part)
            if preview is not None:
                jsob["previewtype"], jsob["preview"] = preview

        if includeContent:
            content = self._GenerateContent(store, uid, part)
            if content is not None:
                jsob["content"] = content

        if part.multipart:
            best = None
            if part.subtype == "alternative":
                best = self._ChooseAlternative(part)

            children = []
            for child in part.children:
                genPreview = best is None or child is best
                children.append(self._PartToJson(store, uid, child, genPreview, includeContent))
            jsob["children"] = children

        return jsob

    def _MessageToJson(self, store, msg):
        uid = msg.uid
        mime = store.Mime(uid)

        if msg.flags & DocFlags.Draft:
            return self._PartToJson(store, uid, mime,
                                    includePreview=False, includeContent=True)
        return self._PartToJson(store, uid, mime)

    def _SetFlags(self, store, convs, flags):
        for guid in convs:
            msgs = [msg for msg in store.Conversation(guid)]
            for msg in msgs:
                self._SetMessageFlags(store, msg, flags)

    def _ClearFlags(self, store, convs, flags):
        for guid in convs:
            msgs = [msg for msg in store.Conversation(guid)]
            for msg in msgs:
                self._ClearMessageFlags(store, msg, flags)

    def _SetMessageFlags(self, store, info, flags):
        if (info.flags & flags) != flags:
            store.Flag(info.uid, flags, FlagMode.Add)

    def _ClearMessageFlags(self, store, info, flags):
        if (info.flags & flags) != 0:
            store.Flag(info.uid, flags, FlagMode.Remove)

    def _GetArchiveDir(self, store, msg):
        # return /mail/archives/yyyy-MM
        date = msg.props["nmap.mail.sent"]
        dt = time.strptime(date, "%Y-%m-%d %H:%M:%SZ")
        return time.strftime("/mail/archives/%Y-%m", dt)

    def _CreateArchiveDirs(self, store, msgs):
        done = {}
        for msg in msgs:
            directory = self._GetArchiveDir(store, msg)
            if not done.has_key(directory):
                store.Create(directory, True)
                done[directory] = 1

    def _MoveToArchiveDirs(self, store, msgs):
        for msg in msgs:
            directory = self._GetArchiveDir(store, msg)
            store.Move(msg.uid, directory)

    def _ArchiveConversations(self, store, guids):
        msgs = []
        for guid in guids:
            for info in store.Conversation(guid, ["nmap.mail.sent"]):
                msgs.append(info)
        self._CreateArchiveDirs(store, msgs)
        self._MoveToArchiveDirs(store, msgs)
        return bongo.dragonfly.OK

    def _UnarchiveConversations(self, store, guids):
        msgs = []
        for guid in guids:
            for info in store.Conversation(guid, ["nmap.mail.sent"]):
                msgs.append(info)
        for msg in msgs:
            store.Move(msg.uid, "/mail/INBOX")
        return bongo.dragonfly.OK

    def _ArchiveMessage(self, store, bongoId):
        msgs = [store.Info(bongoId, ["nmap.mail.sent"])]
        self._CreateArchiveDirs(store, msgs)
        self._MoveToArchiveDirs(store, msgs)
        return bongo.dragonfly.OK

    def _UnarchiveMessage(self, store, bongoId):
        store.Move(bongoId, "/mail/INBOX")
        return bongo.dragonfly.OK

    def _StarConversations(self, store, uids):
        for uid in uids:
            store.Flag(uid, DocFlags.Starred, FlagMode.Add)
        return bongo.dragonfly.OK

    def _UnstarConversations(self, store, uids):
        for uid in uids:
            store.Flag(uid, DocFlags.Starred, FlagMode.Remove)
        return bongo.dragonfly.OK

    def _SendPart(self, store, req, uid, part):
        stream = store.ReadMimePart(uid, part)
        decoder = self._BuildDecoder(stream, part, False)

        ct = part.contenttype
        if part.charset is not None:
            ct = "%s; charset=%s" % (ct, part.charset)

        req.content_type = ct
        req.write(decoder.read())
        return bongo.dragonfly.OK

    def _FindPart(self, part, name):
        print `part`
        if part.name == name:
            return part

        if len(part.children) > 0:
            for child in part.children:
                ret = self._FindPart(child, name)
                if ret is not None:
                    return ret

        return None

    def _SendMessage(self, store, req, rp):
        msgId = rp.handlerData["message"]
        if rp.extension is None:
            # send raw data
            msg = store.Read(msgId)

            req.headers_out["Content-Length"] = str(len(msg))
            req.content_type = "message/rfc822"

            req.write(msg)

            return bongo.dragonfly.OK

        return bongo.dragonfly.HTTP_NOT_FOUND

    def _SendAttachment(self, store, req, rp):
        name = rp.handlerData["attachment"]
        if rp.extension:
            name += "." + rp.extension
        
        msg = rp.handlerData["message"]

        parts = store.Mime(msg)

        if parts is None :
            return bongo.dragonfly.HTTP_NOT_FOUND

        part = self._FindPart(parts, name)

        if part is None:
            return bongo.dragonfly.HTTP_NOT_FOUND

        return self._SendPart(store, req, msg, part)

    def _DiscardDraft(self, store, convid, msgid):
        store.Delete(msgid)
        return bongo.dragonfly.OK

    def _RemoveAttachment(self, store, msgid, attachment) :
        composer = StoreComposer()
        composer.ReadFromStore(store, msgid)
        composer.RemoveAttachment(attachment)
        composer.WriteToStore(store, msgid)
        return bongo.dragonfly.OK

    def _ParseResource(self, rp, resource) :
        if resource is not None :
            resourceLen = len(resource)
            
            if resourceLen > 0 :
                rp.handlerData["conversation"] = resource.pop(0)
            if resourceLen > 1 :
                rp.handlerData["message"] = resource.pop(0)
            if resourceLen > 2 :
                rp.handlerData["attachment"] = resource.pop(0)
            
        if not rp.handlerData.has_key("conversation") :
            if rp.page is None:
                start = end = -1
            else:
                start = self.pageSize * (rp.page - 1)
                end = start + self.pageSize - 1

            rp.handlerData["start"] = start
            rp.handlerData["end"] = end        
        
    def ParsePath(self, rp) :
        if (rp.resource) :        
            self._ParseResource(rp, rp.resource.split("/"))
        else :
            self._ParseResource(rp, None)
            

    def do_GET(self, req, rp):
        store = self.GetStoreClient(req, rp)

        try:
            if rp.handlerData.has_key("attachment") :
                return self._SendAttachment(store, req, rp)
            elif rp.handlerData.has_key("message") :
                return self._SendMessage(store, req, rp)
            elif rp.handlerData.has_key("conversation") :
                return self._SendJson(req, self._GetConversation(store, rp, [ 1, 1, 1, 1 ]))
            else :
                return self._SendJson (req, self._GetConversationList(store, rp,
                                                                      [rp.handlerData["start"], rp.handlerData["end"],
                                                                       rp.handlerData["start"], rp.handlerData["end"],
                                                                       True]))
        finally :
            store.Quit()

    def _PostToAttachment(self, req, rp) :
        raise HttpError(401, "No methods against attachments")

    def _CreateAttachment(self, req, rp, store, fields) :
        req.log.error("creating attachment")
        msgid = rp.handlerData["message"]

        payload = ExistingFilePayload(fields["attachment-file"].file)
        
        composer = StoreComposer()
        composer.ReadFromStore(store, msgid)

        field = fields["attachment-file"]
        composer.Attach(payload, field.filename, field.headers["Content-Type"].strip(), "8bit")

        composer.WriteToStore(store, msgid)

        ret = { "id": int(fields["id"].value),
                "error": None,
                "result": { "attachId" : field.filename,
                            "mimeType" : field.type} }
        return self._SendJson(req, ret)
        

    def _PostFormToMessage(self, req, rp, store, fields) :
        req.log.error("posting form to message")
        method = fields["method"].value

        if method == "createAttachment" :
            self._CreateAttachment(req, rp, store, fields)
            return bongo.dragonfly.OK
        
    def _PostJsonToMessage(self, req, rp, store, jsob) :
        msgid = rp.handlerData["message"]
        method = jsob.get("method")
        
        if method == "delete":
            store.Flag(msgid, DocFlags.Deleted, FlagMode.Add)
            return bongo.dragonfly.OK
        elif method == "undelete":
            store.Flag(msgid, DocFlags.Deleted, FlagMode.Remove)
            return bongo.dragonfly.OK

        elif method == "junk":
            store.Flag(msgid, DocFlags.Junk, FlagMode.Add)
            return bongo.dragonfly.OK
        elif method == "unjunk":
            store.Flag(msgid, DocFlags.Junk, FlagMode.Remove)
            return bongo.dragonfly.OK

        elif method == "archive":
            self._ArchiveMessage(store, msgid)
            return bongo.dragonfly.OK
        elif method == "unarchive":
            self._UnarchiveMessage(store, msgid)
            return bongo.dragonfly.OK

        elif method == "star":
            store.Flag(msgid, DocFlags.Starred, FlagMode.Add)
            return bongo.dragonfly.OK                
        elif method == "unstar":
            store.Flag(msgid, DocFlags.Starred, FlagMode.Remove)
            return bongo.dragonfly.OK

        elif method == "updateDraft":
            return self._UpdateDraft(store, req, jsob, msgid)
        elif method == "send":
            if jsob.has_key("params") and jsob["params"][0] :
                self._UpdateDraft(store, req, jsob, msgid, True)
            return self._DeliverMessage(store, req, jsob, rp)            

        return bongo.dragonfly.HTTP_NOT_FOUND

    def _PostToMessage(self, req, rp) :
        contentType = req.headers_in["Content-Type"]

        length = int(req.headers_in["Content-Length"])
        req.log.debug("ConversationsHandler._PostToMessage (%d bytes)", length)

        store = self.GetStoreClient(req, rp)
        try:

            if contentType.startswith("multipart/form-data") :
                return self._PostFormToMessage(req, rp, store, req.form)
            else :
                doc = req.read(length)
                jsob = self._JsonToObj(doc)
                return self._PostJsonToMessage(req, rp, store, jsob)
        finally:
            store.Quit()

    def _PostToConversation(self, req, rp) :
        length = int(req.headers_in["Content-Length"])
        req.log.debug("ConversationsHandler._PostToConversation (%d bytes)", length)
        doc = req.read(length)

        jsob = self._JsonToObj(doc)

        store = self.GetStoreClient(req, rp)
        method = jsob.get("method")

        print "method: %s" % (method)

        try:
            if method == "delete":
                return self._SetFlags(store, jsob.get("bongoIds"), DocFlags.Deleted)
            elif method == "undelete":
                return self._ClearFlags(store, jsob.get("bongoIds"), DocFlags.Deleted)

            elif method == "junk":
                return self._SetFlags(store, jsob.get("bongoIds"), DocFlags.Junk)
            elif method == "unjunk":
                return self._ClearFlags(store, jsob.get("bongoIds"), DocFlags.Junk)

            elif method == "archive":
                return self._ArchiveConversations(store, jsob.get("bongoIds"))
            elif method == "unarchive":
                return self._UnarchiveConversations(store, jsob.get("bongoIds"))

            elif method == "star":
                return self._StarConversations(store, jsob.get("bongoIds"))
            elif method == "unstar":
                return self._UnstarConversations(store, jsob.get("bongoIds"))

            # message draft/send
            elif method == "createDraft":
                return self._CreateDraft(store, req, jsob)
            elif method == "getConversationList":
                return self._SendConversationList(store, req, jsob, rp)
            elif method == "getConversation":
                return self._SendConversation(store, req, jsob, rp)

            return bongo.dragonfly.HTTP_NOT_FOUND
        finally:
            store.Quit()

    def do_POST(self, req, rp):
        if rp.handlerData.has_key("attachment") :
            return self._PostToAttachment(req, rp)
        elif rp.handlerData.has_key("message") :
            return self._PostToMessage(req, rp)
        else :
            return self._PostToConversation(req, rp)


    def do_DELETE(self, req, rp):
        if rp.resource is None:
            return bongo.dragonfly.HTTP_NOT_FOUND

        try :
            convid = rp.handlerData["conversation"]
            msgid = rp.handlerData["message"]
        except KeyError:
            return bongo.dragonfly.HTTP_NOT_FOUND

        store = self.GetStoreClient(req, rp)

        try:
            if rp.handlerData.has_key("attachment") :
                return self._RemoveAttachment(store, msgid, rp.handlerData["attachment"])
            else :
                return self._DiscardDraft(store, convid, msgid)
        finally:
            store.Quit()

    def GetConversationList(self,
                            store,
                            collection,
                            queries = None,
                            idStart = -1,
                            idEnd = -1,
                            propsStart = -1,
                            propsEnd = -1,
                            showTotal = False,
                            flags = None,
                            mask = None) :
        props = ["bongo.from",
                 "nmap.conversation.count",
                 "nmap.conversation.date",
                 "nmap.conversation.subject",
                 "bongo.conversation.subject",
                 "nmap.conversation.unread"]

        if (queries is not None and len(queries) > 0) :
            query = " OR ".join (map(lambda q: "(%s)" % (q), queries))
        else :
            query = None

        storeConvsIter = store.Conversations(collection, props, query,
                                             start=idStart, end=idEnd,
                                             showTotal=showTotal,
                                             flags=flags, mask=mask)

        # read them all from the iterator
        storeConvs = [ conv for conv in storeConvsIter ]

        idx = idStart-1

        convs = []
        for conv in storeConvs:
            idx = idx + 1

            cob = {"bongoId" : conv.uid}

            if (idx >= propsStart and idx <= propsEnd) or (propsStart == -1 and propsEnd == -1):
                # conv.props = store.PropGet(conv.uid)
                cob["props"] = self._PropsToJson(conv.props)
                cob["props"]["flags"] = self.DocumentFlags(conv.flags)

            convs.append(cob)

        ret = {}
        if showTotal:
            ret["total"] = storeConvsIter.total
            ret["pageSize"] = self.pageSize
        ret["conversations"] = convs

        if propsStart == -1:
            propsStart = 0;
        ret["propsStart"] = propsStart
        
        if propsEnd == -1:
            propsEnd = storeConvsIter.total - 1
        ret["propsEnd"] = min(propsEnd, len(convs)-1)

        return ret

    def _GetQuery(self, store, rp) :
        return None

    def _GetConversationList(self, store, rp, params, queriesIn=[]):
        collection = "/".join(rp.collection)

        queries = queriesIn

        idStart, idEnd, propsStart, propsEnd, showTotal = params

        if (rp.query) :
            queries = []
            queries.extend (queriesIn)
            queries.append(rp.query)

        handlerQuery = self._GetQuery(store, rp)
        if (handlerQuery) :
            if queries is queriesIn:
                queries = []
                queries.extend (queriesIn)
            queries.append(handlerQuery)

        return self.GetConversationList(store, collection, queries,
                                        idStart, idEnd, propsStart, propsEnd,
                                        showTotal)
    
    def _GetConversation(self, store, rp, params):
        collection = "/".join(rp.collection)
        uid = rp.handlerData["conversation"]

        idStart, idEnd, propsStart, propsEnd = params

        props = ["bongo.from",
                 "nmap.conversation.count",
                 "nmap.conversation.date",
                 "nmap.conversation.subject",
                 "bongo.conversation.subject",
                 "nmap.conversation.unread"]

        hq = rp.GetHandler()._GetQuery(store, rp)
        if hq is not None:
            if rp.query is not None:
                query = " AND ".join ([ hq, rp.query ])
            else:
                query = hq
        else:
            query = rp.query

        storeConvsIter = store.Conversations(collection, props, query,
                                             start=idStart, end=idEnd,
                                             center=uid)

        # read them all from the iterator
        storeConvs = []
        idx = 0
        convIdSet = False
        for conv in storeConvsIter:
            if conv.uid == uid:
                convIdx = idx
                convIdSet = True
            storeConvs.append(conv)
            idx = idx + 1

        if not convIdSet:
            convIdx = propsStart = propsEnd = -1;
        elif convIdx < propsStart:
            propsEnd = propsEnd + propsStart - 1
            propsStart = 0
        else:
            propsStart = convIdx - propsStart
            propsEnd = convIdx + propsEnd

        idx = -1
        convs = []
        for conv in storeConvs:
            idx = idx + 1
            cob = {"bongoId" : conv.uid}

            if idx >= propsStart and idx <= propsEnd:
                cob["props"] = self._PropsToJson(conv.props)
                cob["props"]["flags"] = self.DocumentFlags(conv.flags)

            if conv.uid == uid:
                msgs = []
                for info in store.Conversation(conv.uid, ["nmap.mail.sent"]):
                    msgs.append(info)
                    msgs.sort(lambda x,y:
                              cmp(x.props.get("nmap.mail.sent"),
                                  y.props.get("nmap.mail.sent")))

                messages = []
                for msg in msgs:
                    msgProps = store.PropGet(msg.uid)
                    messages.append(self._MsgToJson(store, msg, msgProps))
                    self._SetMessageFlags(store, msg, DocFlags.Seen)

                cob["messages"] = messages

            convs.append(cob)

        ret = {}
        ret["convIdx"] = convIdx
        ret["conversations"] = convs
        ret["propsStart"] = propsStart
        ret["propsEnd"] = min (propsEnd, len(convs) - 1)

        return ret

    def _SendConversationList(self, store, req, jsob, rp):
        ret = {"id" : jsob.get("id"),
               "error" : None,
               "result" : None}

        result = self._GetConversationList(store, rp, jsob.get("params"))
        ret["result"] = result

        return self._SendJson(req, ret)

    def _SendConversation(self, store, req, jsob, rp):
        ret = {"id" : jsob.get("id"),
               "error" : None,
               "result" : None}

        result = self._GetConversation(store, rp, jsob.get("params"))
        ret["result"] = result

        return self._SendJson(req, ret)

class ContactsHandler(ConversationsHandler):
    groupSize = 8
    def ParsePath (self, rp) :
        if (rp.resource) :
            resource = rp.resource.split("/")
            if len(resource) > 0:
                rp.handlerData["contact"] = resource.pop(0)
        
            self._ParseResource(rp, resource)

    def GetContactObjectQuery (self, jsob) :
        a = AddressbookContactsHandler.GetContactAddresses(jsob)
        if not len(a):
            return None
        
        queryPieces = map(lambda address: "nmap.summary.from:\"%s\"" % (address), a)

        return " OR ".join(queryPieces);        

    def GetContactQuery (self, store, bongoId) :
        c = store.Info(bongoId,
                       ["nmap.document"])
        jsob = self._JsonToObj(c.props["nmap.document"])
        return self.GetContactObjectQuery(jsob)

    def _GetContactList(self, store) :
        contacts = store.List("/addressbook/personal", ["nmap.document"])
        ret = []
        
        for contact in contacts :
            obj = self._JsonToObj(contact.props["nmap.document"])
            obj["flags"] = self.DocumentFlags(contact.flags)
            obj["bongoId"] = contact.uid
            ret.append(obj)

        ret.sort(AddressbookContactsHandler.CompareContacts)
        return ret

    def do_GET(self, req, rp):
        if rp.handlerData.has_key("contact") :
            return ConversationsHandler.do_GET(self, req, rp)

        store = self.GetStoreClient(req, rp)

        try:
            ret = []
            contacts = self._GetContactList(store)
            start = self.groupSize * (rp.page - 1)
            end = start + self.groupSize 
            if len(contacts) <= end :
                end = len(contacts)

            if start > end :
                raise HttpError(404, "Page does not exist")
            
            for i in range(start, end) :
                contact = contacts[i]
                query = self.GetContactObjectQuery(contact)
                if query:
                    conversations = self._GetConversationList(store, rp,
                                                              [0, 5, 0, 5,
                                                               True], [query])
                else:
                    conversations = { "total": 0, "propsStart": 0, "propsEnd": -1, "conversations": [] }

                conversations["flags"] = contact["flags"]
                conversations["contact"] = contact["bongoId"]
                ret.append(conversations)

            ret = { "total": len(contacts),
                    "pageSize": self.groupSize,
                    "contacts": ret }
            return self._SendJson(req, ret)
        finally:
            store.Quit()

    def _GetQuery (self, store, rp) :
        if rp.handlerData.has_key("contact") :
            return self.GetContactQuery (store, rp.handlerData["contact"])
        else :
            return None

class ToMeHandler(ConversationsHandler):
    def _GetMyContact(self, store):
        email = msgapi.GetUserEmailAddress(store.owner)
        try:
            prefs = self._JsonToObj(store.Read("/preferences/dragonfly"))
            if not prefs.has_key("addressbook") \
                   or not prefs["addressbook"].has_key("me"):
                return [ email ]

            jsob = self._JsonToObj(store.Read(prefs["addressbook"]["me"]))
        except:
            return [ email ]

        emails = AddressbookContactsHandler.GetContactAddresses(jsob)
        for address in emails:
            if address == email:
                return emails
        emails.append(email)
        return emails

    def GetToMeQuery (self, store) :
        a = self._GetMyContact(store)

        if a is None:
            return None
            
        queryPieces = map(lambda address: "nmap.summary.to:\"%s\"" % (address), a)

        return " OR ".join(queryPieces)
        
    def _GetQuery (self, store, rp) :
        return self.GetToMeQuery(store)

class SubscriptionsHandler(ConversationsHandler):
    groupSize = 8
    def ParsePath (self, rp) :
        if rp.resource is None :
            self._ParseResource(rp, rp.resource)
            rp.handlerData["listid"] = None
            return
        
        resource = rp.resource.split("/")
        if len(resource) > 0:
            rp.handlerData["listid"] = resource.pop(0)
        else :
            raise HttpError(bongo.dragonfly.HTTP_NOT_FOUND) 
        
        self._ParseResource(rp, resource)

    def _GetQuery (self, store, rp) :
        if rp.handlerData["listid"]:
            return "nmap.summary.listid:\"<%s>\"" % (rp.handlerData["listid"])
        return None

    def _SendSubscriptionsList(self, store, req, rp):
        collection = "/".join(rp.collection)
        subscriptions = []
        try:
            subscriptions = []
            for subs in store.MailingLists(collection):
                subscriptions.append(subs.message)
            subscriptions.sort()

            start = self.groupSize * (rp.page - 1)
            end = start + self.groupSize
            if len(subscriptions) <= end:
                end = len(subscriptions)

            if start > end:
                raise HttpError(404, "Page does not exist")

            ret = []
            for i in range(start, end):
                mlist = subscriptions[i]
                query = "nmap.summary.listid:\"%s\"" % mlist

                conversations = self._GetConversationList(store, rp,
                                                          [0, 5, 0, 5, True],
                                                          [query]);

                conversations["listId"] = mlist[1:-1]
                ret.append(conversations)

            ret = { "total": len(subscriptions),
                    "pageSize": self.groupSize,
                    "subscriptions": ret }
            return self._SendJson(req, ret)
        except CommandError, e:
            if e.code == 4240:
                return bongo.dragonfly.Auth.DenyAccess(req)
            else:
                raise

    def do_GET(self, req, rp):
        if rp.resource is not None:
            # use the normal conversations list behavior
            return ConversationsHandler.do_GET(self, req, rp)

        # otherwise, return a list that maps lists to their conversations
        store = self.GetStoreClient(req, rp)

        try:
            return self._SendSubscriptionsList(store, req, rp)
        finally:
            store.Quit()
