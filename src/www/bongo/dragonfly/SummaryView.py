from bongo.store.StoreClient import StoreClient, DocFlags, DocTypes
from bongo.dragonfly.ResourceHandler import ResourceHandler, JSONWrapper
from bongo.StreamIO import Stream
from bongo import BongoError
from AddressbookView import ContactsHandler
from CalendarView import EventsHandler
from MailView import ConversationsHandler, ToMeHandler
from MailView import ContactsHandler as MailContactsHandler
import bongo.dragonfly
from email.Utils import parseaddr
#from datetime import datetime, timedelta, time
import datetime
from libbongo.libs import cal, calcmd, bongojson, JsonArray, JsonObject, msgapi

import sys

class SummaryHandler(ResourceHandler):
    def _ConversationListToJson(self, store, convs):
        props = ["bongo.from",
                 "nmap.conversation.count",
                 "nmap.conversation.date",
                 "nmap.conversation.subject",
                 "bongo.conversation.subject",
                 "nmap.conversation.unread"]

        info = []
        for uid in convs:
            info.append(store.Info(uid, props))

        h = ConversationsHandler()
        return h._ConversationsToJson(info)

    def _AccumulateContactAddresses(self, jsob, contacts):
        addresses = contacts.setdefault("addresses", {})

        for a in ContactsHandler.GetContactAddresses(jsob):
            addresses[a.strip()] = jsob
    
    def _GetMyContact(self, store, contacts):
        try:
            prefs = self._JsonToObj(store.Read("/preferences/dragonfly"))
            if not prefs.has_key("addressbook") \
                   and not prefs["addressbook"].has_key("me"):
                return

            jsob = self._JsonToObj(store.Read(prefs["addressbook"]["me"]))
        except:
            return

        for a in ContactsHandler.GetContactAddresses(jsob):
            contacts.setdefault("myaddresses", {})[a] = True

    def _AccumulateFromMyContacts(self, msg, contacts, summary):
        fmc = summary.setdefault("frommycontacts", {})

        a = contacts.get("addresses")
        if a is None or len(a) == 0:
            return False

        if not msg.props.has_key("bongo.from"):
            return False

        me = contacts.get("myaddresses", {})

        ret = False

        f = ContactsHandler.ParseAddresses(msg.props.get("bongo.from"))
        for name, email in f:
            if me.has_key(email):
                continue

            c = a.get(email)
            if c is not None:
                uid = c["uid"]

                if fmc.has_key(uid):
                    fmc[uid]["count"] = fmc[uid]["count"] + 1
                else:
                    fmc[uid] = {"fn" : c.get("fn"),
                                "count" : 1,
                                "flags": c.get("flags")}
                ret = True

        return ret

    def _AccumulateLists(self, msg, summary):
        listid = msg.props.get("bongo.listid")
        if not listid:
            return False

        listid = listid.strip()
        name, email = parseaddr(listid)
        displayName = email

        try:
            idx = displayName.index("@")
            if idx != -1:
                displayName = displayName[:idx]
        except:
            pass

        lists = summary.setdefault("lists", {})

        if lists.has_key(email):
            lists[email]["count"] = lists[email].get("count", 0) + 1
        else:
            lists[email] = {"listId" : email,
                            "displayName" : displayName,
                            "count" : 1}

        return True

    def _AccumulateOther(self, msg, summary):
        other = summary.setdefault("other", {})
        convid = msg.props.get("nmap.mail.conversation")
        other[convid] = other.get(convid, 0) + 1

        return True

    def _SummarizeMail(self, store, ret) :
        contacts = {}

        handler = ConversationsHandler()

        flags, mask = DocFlags.GetMask({DocFlags.Deleted : False,
                                        DocFlags.Seen : False,
                                        DocFlags.Starred : True})
        ret["starred"] = handler.GetConversationList(store, "inbox",
                                                     None, 0, 15, 0, 15, True,
                                                     flags=flags, mask=mask)

        handler = ToMeHandler()

        flags, mask = DocFlags.GetMask({DocFlags.Deleted : False,
                                        DocFlags.Seen : False})

        q = handler.GetToMeQuery(store)
        if q:
            queries = [q]
        else:
            queries = None

        ret["tome"] = handler.GetConversationList(store, "inbox",
                                                  queries,
                                                  0, 15, 0, 15, True,
                                                  flags=flags, mask=mask)

        self._GetMyContact(store, contacts)

        for c in store.List("/addressbook/personal", ["nmap.document"]):
            jsob = self._JsonToObj(c.props["nmap.document"])
            jsob["uid"] = c.uid
            jsob["flags"] = self.DocumentFlags(c.flags)

            self._AccumulateContactAddresses(jsob, contacts)

        props = ["bongo.from",
                 "bongo.listid",
                 "bongo.to",
                 "nmap.mail.conversation"]

        for msg in store.List("/mail/INBOX", props):
            if msg.flags & DocFlags.Deleted \
               or (msg.flags & DocFlags.Seen
                   and not msg.flags & DocFlags.Draft):
                continue

            if msg.type != DocTypes.Mail:
                continue

            handled = False
            if self._AccumulateFromMyContacts(msg, contacts, ret):
                handled = True

            if self._AccumulateLists(msg, ret):
                handled = True

            if not handled and not msg.flags & DocFlags.Seen:
                self._AccumulateOther(msg, ret)

        if ret.has_key("other"):
            ret["other"] = self._ConversationListToJson(store, ret["other"].keys())

    def _SummarizeCalendar(self, store, ret, now, then) :
        view = EventsHandler()        

        # FIXME: timezone!
        jsob = view.GetEventsJson (store, self._SystemTimezone(), now, then);
        ret["events"] = JSONWrapper(str(jsob))

    def _FindAttachment(self, part, contenttype) :
        if part.contenttype == contenttype :
            return part
        if part.multipart :
            for child in part.children :
                ret = self._FindAttachment(child, contenttype)
                if ret :
                    return ret
        return None

    def _GetAttachment(self, store, msg, part) :
        stream = store.ReadMimePart(msg.uid, part)
        decoder = Stream(stream)
        if part.encoding is not None :
            decoder.AddCodec(part.encoding)
        if part.charset is not None :
            decoder.AddCodec(part.contenttype)
        return decoder.read()

    def _SummarizeInvitations(self, req, store, ret, now, then) :
        eventInvitations = []
        props = ["bongo.from",
                 "bongo.subject",
                 "nmap.mail.conversation"]
        msgs = list(store.ISearch("nmap.mail.attachmentmimetype:text/calendar AND nmap.flags:inbox", props, 0, 20))
        mailHandler = ConversationsHandler()

        for msg in msgs :
            mime = store.Mime(msg.uid)
            part = self._FindAttachment(mime, "text/calendar")
            if part :
                jsob = { "from" : mailHandler.FormatAddresses(msg.props.get("bongo.from", "")),
                         "subject" : msg.props.get("bongo.subject", ""),
                         "convId" : msg.props.get("nmap.mail.conversation", ""),
                         "bongoId" : msg.uid }
                                
                event = self._GetAttachment(store, msg, part)
                print "got %s" %(event)
                try :
                    jcal = cal.IcalToJson(event)
                    if jcal :
                        jsob["jcal"] = JSONWrapper (str(jcal))
                        # FIXME: timezone
                        occs = [ JSONWrapper(str(cal.PrimaryOccurrence(jcal, self._SystemTimezone()))) ]
                        jsob["occurrences"] = occs
                        eventInvitations.append(jsob)
                except RuntimeError, e:
                    req.log.warning(e)

        ret["eventInvites"] = eventInvitations

        calInvitations = []
        msgs = list(store.ISearch("nmap.mail.attachmentmimetype:\"text/x-bongo-invitation\" AND nmap.flags:inbox", props, 0, 20))
        for msg in msgs :
            mime = store.Mime(msg.uid)
            part = self._FindAttachment(mime, "text/x-bongo-invitation")
            if not part :
                continue
            
            calendar = self._GetAttachment(store, msg, part)
            invitation = None
            try :
                invitation = self._JsonToObj(calendar)
            except :
                req.log.warning(sys.exc_info())
                continue

            invitation["from"] = mailHandler.FormatAddresses(msg.props.get("bongo.from", "")),
            invitation["subject"] = msg.props.get("bongo.subject", "")
            invitation["convId"] = msg.props.get("nmap.mail.conversation", "")
            invitation["bongoId"] = msg.uid
            
            calInvitations.append(invitation)

        ret["calInvites"] = calInvitations

    def _SendSummary(self, store, req, rp):
        ret = {}

        # there comes a time when you must go
        now = datetime.datetime.now();
        then = datetime.datetime.combine (now + datetime.timedelta(14 - now.isoweekday() % 7), datetime.time (0, 0, 0))

        self._SummarizeMail(store, ret)
        self._SummarizeCalendar(store, ret, now, then)
        self._SummarizeInvitations(req, store, ret, now, then)

        return self._SendJson(req, ret)
    
    def do_GET(self, req, rp):
        store = self.GetStoreClient(req, rp)
        try:
            return self._SendSummary(store, req, rp)
        except:
            store.Quit()
            raise
