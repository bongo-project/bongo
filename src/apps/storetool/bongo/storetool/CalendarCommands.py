import bongo.external.simplejson as simplejson
import bongo.external.vobject as vobject
import logging
import os
import re
import time
import random
import md5

import email
from email.MIMEText import MIMEText
from email.MIMEMessage import MIMEMessage
from email.MIMEMultipart import MIMEMultipart
from email.Message import Message

import bongo.table as table

from bongo.cmdparse import Command
from bongo.Contact import Contact
from bongo.BongoError import BongoError
from libbongo.libs import bongojson, msgapi
from bongo.store.StoreClient import DocTypes, StoreClient, CalendarACL
from bongo.store.QueueClient import QueueClient

class CalendarsCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "calendar-list", aliases=["cl"],
                         summary="List the calendars in your store")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store)

        cols = ["subd?", "Name", "Url"]
        rows = []

        try:
            cals = list(store.List("/calendars", props=["bongo.calendar.url"]))
            for cal in cals:
                subd = cal.props.has_key("bongo.calendar.url") and "Yes" or None
                rows.append([subd, cal.filename,
                             cal.props.get("bongo.calendar.url")])
        finally:
            store.Quit()

        print table.format_table(cols, rows)

class CalendarEventsCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "calendar-events", aliases=["ce"],
                         summary="List the events in a calendar",
                         usage="%prog %cmd <calendar>")

    def _FindCalendar(self, store, name):
        cals = list(store.List("/calendars"))
        for cal in cals:
            if cal.filename == name:
                return cal

    def Run(self, options, args):
        store = StoreClient(options.user, options.store)

        cols = ["Summary", "Start", "End"]
        rows = []

        try:
            cal = self._FindCalendar(store, args[0])
            if cal is None:
                print "Could not find calendar named '%s'" % args[0]
                return

            events = list(store.Events(cal.uid, ["nmap.document",
                                                 "nmap.event.calendars"]))

            for event in events:
                jsob = simplejson.loads(event.props["nmap.document"].strip())
                comp = jsob["components"][0]

                summary = comp.get("summary")
                if summary is not None:
                    summary = summary.get("value")

                start = comp.get("start")
                if start is not None:
                    start = start.get("value")

                end = comp.get("end")
                if end is not None:
                    end = end.get("value")

                rows.append((summary, start, end))
        finally:
            store.Quit()

        rows.sort()
        print table.format_table(cols, rows)

class EventsDeleteCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "events-delete", aliases=["ed"],
                         summary="Delete all events in the store")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store)

        try:
            events = list(store.Events())

            for event in events:
                cals = store.PropGet(event.uid, "nmap.event.calendars")
                cals = cals.strip().split("\n")
                for cal in cals:
                    if cal == "":
                        continue
                    print "unlinking event", event.uid
                    store.Unlink(cal, event.uid)
                store.Delete(event.uid)
        finally:
            store.Quit()

class EventsCleanupCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "events-cleanup", aliases=["ec"],
                         summary="Delete any events not linked with calendars")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store)

        try:
            events = list(store.Events())

            for event in events:
                cals = store.PropGet(event.uid, "nmap.event.calendars")
                if cals is None or cals == "":
                    print "deleting event", event.uid
                    store.Delete(event.uid)
        finally:
            store.Quit()

class CalendarDeleteCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "calendar-delete", aliases=["cd"],
                         summary="Delete specified calendars",
                         usage="%prog %cmd <calendars>")


    def _FindCalendar(self, store, name):
        cals = list(store.List("/calendars"))
        for cal in cals:
            if cal.filename == name:
                return cal

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)

        try:
            for calname in args:
                cal = self._FindCalendar(store, calname)
                if cal is None:
                    print "Could not find calendar named '%s'" % calname
                    continue
    
                if calname.lower() != "personal":
                    print "deleting calendar", cal.uid
                    store.Delete(cal.uid)
                else:
                    print "events deleted. not deleting calendar", calname
        finally:
            store.Quit()

class CalendarSubscribeCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "calendar-sub", aliases=["cs"],
                         summary="Subscribe to specified calendar",
                         usage="%prog %cmd <name> <url>")

    def Run(self, options, args):
        if len(args) < 2:
            self.print_usage()
            self.exit()

        (name, url) = args
        uid = msgapi.IcsSubscribe(options.store, name, None, url, None, None)
        print "Subscribed: uid is", hex(uid)

class CalendarImportCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "calendar-import", aliases=["ci"],
                         summary="Import a specified calendar file",
                         usage="%prog %cmd <name> <file>")

    def Run(self, options, args):
        if len(args) < 2:
            self.print_usage()
            self.exit()

        (name, file) = args

        if re.search("^[^/]+://", file):
            url = file
        else:
            file = os.path.realpath(file)

            if not os.access(file, os.R_OK):
                self.exit("File doesn't exist or isn't readable: %s" % file)

            url = "file://" + file

        uid = msgapi.IcsImport(options.store, name, None, url, None, None)
        print "Imported: uid is", hex(uid)
        
class CalendarPublishCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    def __init__(self):
        Command.__init__(self, "calendar-publish", aliases=["cp"],
                         summary="Make a specified calendar public, and send an invitation",
                         usage="%prog %cmd <name> [address, ...]")

    def Run(self, options, args):
        if len(args) < 1 :
            self.print_usage()
            self.exit()

        doc = "\"/calendars/%s\"" % (args[0])
        addresses = args[1:]

        store = StoreClient(options.user, options.store)

        try:
            acl = CalendarACL(store.GetACL(doc))
            acl.SetPublic(CalendarACL.Rights.Read)
            store.SetACL(doc, acl.GetACL())
            
        finally:
            store.Quit()

class CalendarUnpublishCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    def __init__(self):
        Command.__init__(self, "calendar-unpublish", aliases=["cup"],
                         summary="Make a specified calendar non-public",
                         usage="%prog %cmd <name> [address, ...]")

    def Run(self, options, args):
        if len(args) < 1 :
            self.print_usage()
            self.exit()

        doc = "\"/calendars/%s\"" % (args[0])
        addresses = args[1:]

        store = StoreClient(options.user, options.store)

        try:
            acl = CalendarACL(store.GetACL(doc))
            acl.SetPublic(0)
            store.SetACL(doc, acl.GetACL())
            print "current acl: %s" % (str(acl))
        finally:
            store.Quit()

class CalendarShareCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    def __init__(self):
        Command.__init__(self, "calendar-share", aliases=["csh"],
                         summary="Share a calendar with a set of addresses",
                         usage="%prog %cmd <name> [address, ...]")


    def _SendMessage(self, options, address, msg) :
        queue = QueueClient(options.user)
        queue.Create()
        queue.StoreFrom(options.user + "@localhost")
        queue.StoreTo(address, address)
        queue.StoreMessage(email.Utils.fix_eols(msg.as_string()))
        queue.Run()
        queue.Quit()

    def _SendInvitation(self, options, address, uid, calName, token, rights) :
        msg = MIMEMultipart()
        msg['Subject'] = "%s has shared a calendar with you" % (options.user)
        msg['From'] = "%s@localhost" % (options.user)
        msg['To'] = address

        description = MIMEMultipart("alternative")
        description.attach(MIMEText("This is a thing"))
        description.attach(MIMEText("<b>This is a thing, in html</b>", "html"))
        
        msg.attach(description)

        invitation = {}
        invitation["owner"] = options.user
        # FIXME: get the server name right
        invitation["server"] = "localhost"
        invitation["calendarName"] = calName
        invitation["calendarId"] = uid
        invitation["token"] = token
        invitation["rights"] = rights
        
        msg.attach(MIMEText(simplejson.dumps(invitation), "x-bongo-invitation"))
                
        print "sending:"
        print msg.as_string()
        self._SendMessage(options, address, msg)

    def Run(self, options, args):
        if len(args) < 1 :
            self.print_usage()
            self.exit()

        doc = "\"/calendars/%s\"" % (args[0])
        addresses = args[1:]

        store = StoreClient(options.user, options.store)

        try:
            info = store.Info(doc)
            acl = CalendarACL(store.GetACL(doc))
            for address in addresses :
                token = acl.Share(address, (CalendarACL.Rights.Read | CalendarACL.Rights.Write))

                self._SendInvitation(options, address, info.uid, args[0], token, "read,write")
                
                print "Shared with %s, token is %s" % (address, token)
            store.SetACL(doc, acl.GetACL())
        finally:
            store.Quit()

class CalendarAcceptShareCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    def __init__(self):
        Command.__init__(self, "calendar-accept-share", aliases=["cash"],
                         summary="Accept a specified calendar invitation",
                         usage="%prog %cmd <calname>")


    def Run(self, options, args):
        if len(args) < 1 or len(args) > 1:
            self.print_usage()
            self.exit()

        try:
            calname, = args
        except ValueError:
            self.print_usage()
            self.exit()
            
        docpath = "\"/calendars/%s\"" % (calname)

        store = StoreClient(options.user, options.user)

        try:
            cal = store.PropGet(docpath)
            if cal["bongo.calendar.invitation"] == "1" :
                store.PropSet(docpath, "bongo.calendar.owner", cal["bongo.calendar.invitation-owner"])
                store.PropSet(docpath, "bongo.calendar.uid", cal["bongo.calendar.invitation-uid"])
                store.PropSet(docpath, "bongo.calendar.token", cal["bongo.calendar.invitation-token"])
                store.PropSet(docpath, "bongo.calendar.invitation", "0")
                print "Accepted %s" % (calname)
            else :
                print "%s is not a calendar invitation" % (calname)

            store.Quit()
        except BongoError, e:
            store.Quit()
            self.exit("%s: %s" % (docpath, str(e)))
        except:
            store.Quit()

class CalendarUnshareCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    def __init__(self):
        Command.__init__(self, "calendar-unshare", aliases=["cush"],
                         summary="Revoke access from a given calendar",
                         usage="%prog %cmd <name> [address, ...]")

    def Run(self, options, args):
        if len(args) < 1 :
            self.print_usage()
            self.exit()

        doc = "\"/calendars/%s\"" % (args[0])
        addresses = args[1:]

        store = StoreClient(options.user, options.store)

        try:
            acl = CalendarACL(store.GetACL(doc))
            if (addresses) :
                for address in addresses :
                    acl.Unshare(address)
            else :
                acl.UnshareAll()
                
            store.SetACL(doc, acl.GetACL())
            print "current acl: %s" % (str(acl))
        finally:
            store.Quit()


class CalendarListSharesCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    def __init__(self):
        Command.__init__(self, "calendar-list-shares", aliases=["cls"],
                         summary="List sharing info for a given calendar",
                         usage="%prog %cmd <name>")

    def Run(self, options, args):
        if len(args) < 1 :
            self.print_usage()
            self.exit()

        doc = "\"/calendars/%s\"" % (args[0])

        store = StoreClient(options.user, options.store)

        try:
            acl = CalendarACL(store.GetACL(doc))

            summary = acl.Summarize()
            print "Public: %s" % (acl.RightsToString(summary["public"]))
                
            print "\nAddresses: "
            print "========== "
            for address in summary["addresses"] :
                info = summary["addresses"][address]
                print address
                print "\tpassword %s" % info["password"]
                print "\taccess: %s" % (acl.RightsToString(info["rights"]))

        finally:
            store.Quit()

