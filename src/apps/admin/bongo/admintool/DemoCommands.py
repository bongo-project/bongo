import bongo.external.simplejson as simplejson
import bongo.external.vobject as vobject
import logging
import os
import re
import time
import random
import md5
import urllib

import email
from email.MIMEText import MIMEText
from email.MIMEMessage import MIMEMessage
from email.MIMEMultipart import MIMEMultipart
from email.Message import Message

import bongo.table as table

from bongo.cmdparse import Command
from bongo.Contact import Contact
from bongo.BongoError import BongoError
from bongo.store.StoreClient import DocTypes, DocFlags, FlagMode, StoreClient
from bongo.store.QueueClient import QueueClient
from bongo import MDB, Xpl

from bongo.admin import Util

import MdbUtil

if os.path.isfile("demo/contacts/list.vcf") :
    demopath = "demo"
else :
    demopath = Xpl.DEFAULT_DATA_DIR + "/demo"

defaultData = {
    "calendarPreferences": {
        "defaultTimezone": "/bongo/America/Boise",
        "timezones": [
            { "name": "Eastern", "tzid": "/bongo/America/New_York" },
            { "name": "Mountain", "tzid": "/bongo/America/Boise" },
            { "name": "France", "tzid": "/bongo/Europe/Paris" } ],
        "disabledCalendars": { }
    },
    "calendars": [
        { "name" : "Red Sox",
          "url" : "http://ical.mac.com/gfreckle/Red%20Sox.ics" },
        { "name" : "Holidays",
          "url" : "http://homepage.mac.com/ical/.calendars/US32Holidays.ics" },
        { "name" : "openSUSE",
          "url" : "http://www.google.com/calendar/ical/8qn58ljtkk7a209fkjn7jb4dd8@group.calendar.google.com/public/basic" },
        ],
    "starMail": ["bongo-general-008"],
    "starContacts": ["Dan Dundero"]
}

class DemoCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "demo",
                         summary="Create a demo user")
        self.add_option("", "--demo-user", type="string", default="rupert",
                        help="demo user to create [default %default]")

    def AddUser(self, mdb, context, user, address) :
        print "adding user %s to %s with password 'bongo'" % (user, context)
        attributes = {}
        attributes[mdb.A_GIVEN_NAME] = ["Rupert"]
        attributes[mdb.A_SURNAME] = ["Monkey"]
        attributes[mdb.A_EMAIL_ADDRESS] = [address]
        Util.AddBongoUser(mdb, context, user, attributes, "bongo")

    def ImportMail(self, store, address, name, demo) :
        dir = os.listdir(demopath + "/mail")
        dir.sort()

        for filename in dir :
            if filename[0] == '.' :
                continue
            print "importing %s" % (filename)
            f = file ("%s/mail/%s" % (demopath, filename))
            data = f.read()
            f.close()
            data = data.replace("@ADDRESS@", address)
            data = data.replace("@NAME@", name)
            
            uid = store.Write("/mail/INBOX", DocTypes.Mail, email.Utils.fix_eols(data))
            if filename in demo["starMail"] :
                conversation = store.PropGet(uid, "nmap.mail.conversation")
                print "starring conversation %s" % (conversation)
                store.Flag(uid, DocFlags.Starred, FlagMode.Add)
                store.Flag(conversation, DocFlags.Starred, FlagMode.Add)

    def CreateMyContact(self, store, address, name) :
        mycontact = { "n": [ {"value":name} ], 
                      "fn": name,
                      "email": [{"type": ["WORK"], "value": address }]
                      }
        return store.Write("/addressbook/personal", 
                           DocTypes.Addressbook, 
                           simplejson.dumps(mycontact))               

    def ImportContacts(self, store, demo) :
        dir = os.listdir(demopath + "/contacts")
        dir.sort()

        for filename in dir :
            if filename[0] == '.' :
                continue

            print "importing %s" % (filename)
            f = open("%s/contacts/%s" % (demopath, filename), 'r')
            for v in vobject.readComponents(f):
                c = Contact(v)
                uid = store.Write("/addressbook/personal", DocTypes.Addressbook,
                                  c.AsJson())
                if c.data["fn"] in demo["starContacts"] :
                    store.Flag(uid, DocFlags.Starred, FlagMode.Add)

    def ImportCalendars(self, storename, demo) :
        from libbongo.libs import msgapi

        dirname = demopath + "/calendars"

        dir = os.listdir(dirname)
        dir.sort()

        for filename in dir :
            if filename[0] == '.' :
                continue

            path = urllib.quote(os.path.abspath(dirname + "/" + filename))

            print "importing %s" % (filename)

            calname = os.path.basename(filename)
            calname = os.path.splitext(filename)[0]
            calname = urllib.unquote(calname)

            msgapi.IcsImport(storename, calname, None, "file://" + path, None, None)

    def run(self, options, args):
        from libbongo.libs import msgapi

        if len(args) > 0:
            self.print_usage()
            self.exit()

        mdb = MdbUtil.GetMdb(options)
        user = options.demo_user

        context, username = MdbUtil.GetContext(user)

        address = "%s@bongo.org" % (user)
        name = "Rupert Monkey"

        try:
            Util.GetUserAttributes(mdb, context, user)
            print "using existing user %s" % (user)
        except :
            print "adding user %s" % (user)
            self.AddUser(mdb, context, user, address)

        dragonfly = { "calendar": defaultData["calendarPreferences"] }
        dragonfly["mail"] = { "from": address }
        print "dragonfly config is: %s" % simplejson.dumps(dragonfly)
        
        try :
            store = StoreClient(user, user)
            # Clear out the existing store
            store.Reset()
            store.Quit()
            
            store = StoreClient(user, user)

            contact = self.CreateMyContact(store, address, name)
            dragonfly["addressbook"] = { "me" : contact }

            store.Write("/preferences", DocTypes.Unknown, simplejson.dumps(dragonfly), filename="dragonfly")

            self.ImportMail(store, address, name, defaultData)
            self.ImportContacts(store, defaultData)
            self.ImportCalendars(user, defaultData)

        finally :
            store.Quit()
