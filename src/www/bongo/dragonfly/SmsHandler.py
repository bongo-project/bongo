from ResourceHandler import HttpHandler
from CalendarView import EventsHandler
from bongo.store.StoreClient import StoreClient, DocTypes
from bongo.CalCmd import Command
from libbongo.libs import cal, calcmd
import bongo.dragonfly
import urllib

class SmsHandler(HttpHandler):
    def do_GET(self, req):
        user = None

        print req.uri
        
        for arg in req.args.split("&"):
            (name, value) = arg.split("=")
            if "data" == name:
                cmdstr = urllib.unquote(value)
                cmdstr = cmdstr.replace("+", " ")
            elif "sender" == name:
                sender = value
                user = "cyrus"  #FIXME: look up user from the sender's phone number

        if (None == user):
            req.write("Couldn't find your calendar!")
            return bongo.dragonfly.OK
        
        store = StoreClient(user, user)
        if (None == store):
            req.write("Sorry, couldn't open your calendar.")
            return bongo.dragonfly.OK

        if (None == cmdstr):
            req.write("Please send a command.")
            return bongo.dragonfly.OK
        
        print "command string %s" % (cmdstr)
        try:
            command = Command(cmdstr, '/bongo/America/New_York')
        except ValueError:
            return bongo.dragonfly.HTTP_BAD_REQUEST

        if command.GetType() == calcmd.NEW_APPT:
            
            jcal = command.GetJson()

            uid = store.Write("/events", DocTypes.Event, str(jcal))
            store.Link("/calendars/Personal", uid)

            req.write("OK, new appt: ")
            req.write(command.GetData());
            req.write(" ");
            req.write(command.GetConciseDateTimeRange());
            return bongo.dragonfly.OK

        elif command.GetType() == calcmd.QUERY:
            timezone = '/bongo/America/New_York'
            print "HELLO"
            print type(command.GetBegin())

            events = EventsHandler().GetEventsJson(store, timezone,
                                                 command.GetBegin(), command.GetEnd())

            for event in events:
                try:
                    summary = event['jcal']['components'][0]['summary']['value']
                    for occurrence in event['occurrences'] :
                        start = occurrence['dtstart']['valueLocal']
                        start = cal.TimeToStringConcise(start)
                        
                        if (None is start or None is summary):
                            continue

                        req.write("%s %s\r\n" % (start, summary))
                except:
                    pass



            
#            events = store.Events("/calendars/Personal",
#                                  ["nmap.event.start", "nmap.event.end",
#                                   "nmap.event.summary"],
#                                  start=command.GetBegin(), end=command.GetEnd())
            
#             for event in events:
#                 start = event.props["nmap.event.start"]
#                 start = cal.TimeToStringConcise(start)
#                 end = event.props["nmap.event.end"]
#                 end = cal.TimeToStringConcise(end)
#                 summary = event.props["nmap.event.summary"]

#                 if (None == start or None == end or None == summary):
#                     continue

#                 req.write("%s %s\r\n" % (start, summary))

        else:
            print "Unexpected Type %d" % command.GetType()
