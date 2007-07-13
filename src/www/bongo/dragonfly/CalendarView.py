from bongo.dragonfly.ResourceHandler import ResourceHandler, JSONWrapper, Template
from bongo.store.StoreClient import StoreClient, DocTypes, ACL, CalendarACL, CommandError
from bongo.CalCmd import Command
from libbongo.libs import cal, calcmd, bongojson, JsonArray, JsonObject, msgapi
from bongo.dragonfly.HttpError import HttpError

import datetime
import bongo.BongoError
import time, random, md5, urllib

class EventsHandler(ResourceHandler):
    def _SendEvent(self, store, req, rp):
        splitid = rp.resource.split(':')
        bongoid = splitid[0]
        if len(splitid) == 1 :
            recurid = None
        else :
            recurid = splitid[1]

        timezone = self._GetTimezone(req)
        if rp.extension == "html":
            return self._SendEventHtml(store, req, rp, bongoid, recurid, timezone)

    def _GetTimezone(self, req) :
        return req.headers_in.get("X-Bongo-Timezone", self._SystemTimezone())

    def _GetTimeRange(self, increment, year, month, day=15) :
        center = datetime.datetime(year, month, day, tzinfo=None)
        if increment == "day" :
            before = datetime.timedelta(days=1)
            after = datetime.timedelta(days=2)
        elif increment == "week" :
            weekday = center.isoweekday() % 7
            before = datetime.timedelta(days=(weekday + 1))
            after = datetime.timedelta(days=(9 - weekday))
        elif increment == "month" :
            first = datetime.datetime(year, month, 1, tzinfo=None)
            if month == 12:
                nextmonth = 1
                nextyear = year + 1
            else:
                nextmonth = month + 1
                nextyear = year
            next = datetime.datetime(nextyear, nextmonth, 1)
            before = datetime.timedelta(days=(day + first.weekday() + 1))
            after = datetime.timedelta(days=(39 - day - next.weekday()))
        elif increment == "upcoming" :
            before = datetime.timedelta(days=1)
            after = datetime.timedelta(days=4)

        return (center - before, center + after)

    def _GetRange(self, resource) :
        (increment, year, month, day) = resource.split('/');
        return self._GetTimeRange(increment, int(year), int(month), int(day))
    
    def _SendEvents(self, store, req, rp):
        start = end = None
        eventProps = ["nmap.document", "nmap.event.calendars"]

        cal = "/".join(rp.collection)
        if cal == "all":
            cal = None
        else:
            info = store.Info("/calendars/" + cal)
            cal = info.uid

        timezone = self._GetTimezone(req)
        if rp.extension == "html":
            return self._SendEventsHtml(store, req, rp, cal, timezone)

        if rp.resource is not None:
            start, end = self._GetRange(rp.resource)

        events = list(store.Events(cal, eventProps, query=rp.query, start=start, end=end))            

        if req.user == rp.user:
            sharedEvents = self._GetSharedEvents(store, req, rp, eventProps, start, end)
	    if sharedEvents :
		events.extend(sharedEvents)

        if rp.extension == "json":
            return self._SendEventsJson(req, events, timezone, start, end)
        elif rp.extension == "ics":
            return self._SendEventsIcs(req, events)

    def _GetSharedEvents(self, store, req, rp, eventProps, start, end):
        events = []

        # find any shared calendars and their events
        cals = store.List("/calendars", ["bongo.calendar.uid",
                                         "bongo.calendar.owner",
                                         "bongo.calendar.token"])

        owners = {}
        for cal in cals:
            if not cal.props.has_key("bongo.calendar.owner"):
                continue

            owner = owners.setdefault(cal.props["bongo.calendar.owner"], {})
            owner.setdefault("cals", []).append(cal)
            owner.setdefault("tokens", [])
            if cal.props.has_key("bongo.calendar.token") :
                owner.setdefault("tokens", []).append(cal.props["bongo.calendar.token"])

            # map remote calendar ids to local ids
            owner.setdefault("uids", {})[cal.props["bongo.calendar.uid"]] = cal.uid

        for owner, cals in owners.items():
            shareStore = StoreClient(req.user, owner, authCookie=req.authCookie)
            for token in cals.get("tokens", []):
                shareStore.Token(token)

            sharedEvents = list(shareStore.Events(None, eventProps, query=rp.query, start=start, end=end))

            subscribedEvents = []

            # rewrite the event calendar ids to match the local user's
            for evt in sharedEvents:
                if not evt.props.has_key("nmap.event.calendars"):
                    continue

                evtCals = evt.props["nmap.event.calendars"].split()

                localCals = []
                for evtCal in evtCals:
                    calUid = cals["uids"].get(evtCal)
                    if calUid :
                        localCals.append(calUid)

                if len(localCals) > 0 :
                    evt.props["nmap.event.calendars"] = " ".join(localCals)
                    subscribedEvents.append(evt)

            shareStore.Quit()
            events.extend(subscribedEvents)
        return events

    def do_GET(self, req, rp):
        store = self.GetStoreClient(req, rp)
        try:
            if (rp.resource is not None) and (rp.resource.find("/") == -1):
                req.log.warning("Sending a single event: %s", rp.resource)
                return self._SendEvent(store, req, rp)
            else:
                req.log.warning("Sending all events")
                return self._SendEvents(store, req, rp)
        finally:
            store.Quit()

    def _ConvertTzid(self, store, req, json, rp) :
        params = json["params"]
        if params < 4 :
            raise HttpError(400, "convertTzid requires 4 parameters")
        jcal = params[0]
        time = params[1]
        tzidFrom = params[2]
        tzidTo = params[3]

        ret = {"id" : json.get("id"),
               "error" : None,
               "result" : None }
        try :
            ret["result"] = cal.ConvertTzid(self._ObjToJson(jcal), time, tzidFrom, tzidTo)
        except RuntimeError, e :
            ret["error"] = str(e)

        return self._SendJson(req, ret)

    def do_POST(self, req, rp):
        length = int(req.headers_in["Content-Length"])
        req.log.debug("EventsHandler.do_POST (%d bytes)", length)
        doc = req.read(length)

        store = self.GetStoreClient(req, rp)
        try:
            json = self._JsonToObj(doc)
            method = json.get("method")

            if method == "parseCommand":
                try :
                    (start, end) = self._GetRange(rp.resource)
                except :
                    start = end = None
                timezone = self._GetTimezone(req)
                return self._ParseCommand(req, store, json.get("command"),
                                          timezone, start, end)
            elif method == "subscribe":
                return self._SubscribeReq(store, req, json, rp)
            elif method == "createEvent":
                return self._CreateEventReq(store, req, json, rp)
            elif method == "update" :
                return self._UpdateEventReq(store, req, json, rp)
            elif method == "convertTzid" :
                # FIXME: this doesn't need a store connection
                return self._ConvertTzid(store, req, json, rp)
        finally:
            store.Quit()

    def do_PUT(self, req, rp):
        length = int(req.headers_in["Content-Length"])
        req.log.debug("CalendarView.do_PUT (%d bytes)", length)
        doc = req.read(length)

        store = self.GetStoreClient(req, rp)
        try:
            if rp.resource is not None:
                req.log.debug("Replacing doc %s", rp.resource)
                store.Replace(rp.resource, doc)
            else:
                return bongo.dragonfly.HTTP_NOT_FOUND
        finally:
            store.Quit()

    def do_DELETE(self, req, rp):
        req.log.debug("Deleting")

        store = self.GetStoreClient(req, rp)
        try:
            if rp.resource is not None:
                req.log.debug("Deleting doc %s", rp.resource)
                store.Delete(rp.resource)
            else:
                return bongo.dragonfly.HTTP_NOT_FOUND
        finally:
            store.Quit()

    def _Subscribe(self, store, req, rp, params):
        req.log.debug("Subscribing")
        calendar, url = params

        uid = msgapi.IcsSubscribe(rp.user, calendar, None, url, None, None)

        req.log.debug("Refreshing url: %s" % url)
        
        return {"bongoId":str(uid)}

    def _SubscribeReq(self, store, req, jsob, rp):
        ret = {"id" : jsob.get("id"),
               "error" : None,
               "result" : None}

        try:
            result = self._Subscribe(store, req, rp, jsob.get("params"))
            ret["result"] = result
        except bongo.BongoError, e:
            ret["error"] = str(e)

        return self._SendJson(req, ret)

    def _CreateEvent(self, store, req, rp, params):
        jsob, links = params

        if links :
            defaultLink = links[0]
        else :
            defaultLink = "/calendars/Personal"
            
        uid = store.Write("/events", DocTypes.Event, self._ObjToJson(jsob), link=defaultLink)
        if links and len(links) > 1 :
            self._UpdateLinks(self, uid, links)
        
        return {"bongoId":str(uid)}

    def _CreateEventReq(self, store, req, jsob, rp):
        ret = {"id" : jsob.get("id"),
               "error" : None,
               "result" : None}

        try:
            req.log.debug("Writing new event doc")
            result = self._CreateEvent(store, req, rp, jsob.get("params"))
            ret["result"] = result
        except bongo.BongoError, e:
            ret["error"] = str(e)

        return self._SendJson(req, ret)


        store = self.GetStoreClient(req, rp)
        try:
            if rp.resource is not None:
                req.log.debug("Replacing doc %s", rp.resource)
                store.Replace(rp.resource, rp.resource)
            else:
                return bongo.dragonfly.HTTP_NOT_FOUND
        finally:
            store.Quit()

    def _UpdateLinks(self, store, bongoId, newCals) :
        if len(newCals) <= 0 :
            return
        
        # FIXME: I'd really like to just send a list of links to the store and have it sort them out
        doc = store.Info(bongoId, ["nmap.event.calendars"])
        existingCals = doc.props.get("nmap.event.calendars", "").split("\n")

        for cal in newCals :
            if cal in existingCals :
                print "keeping existing link for %s" % (cal)
                existingCals.remove(cal)
            else :
                print "adding a link for %s" % (cal)
                store.Link(cal, bongoId)

        for cal in existingCals :
            if cal and cal != "":
                store.Unlink(cal, bongoId)

    def _UpdateEventReq(self, store, req, jsob, rp):
        ret = {"id" : jsob.get("id"),
               "error" : None,
               "result" : None}

        params = jsob.get("params")
        jcal = params[0]
        links = params[1]

        try:
            if rp.resource is not None :
                req.log.debug("Replacing doc %s", rp.resource)
                store.Replace(rp.resource, self._ObjToJson(jcal))
                
                if links :
                    self._UpdateLinks(store, rp.resource, links)
                
                ret["result"] = "Success"
            else :
                ret["error"] = "No event specified"
        except bongo.BongoError, e:
            ret["error"] = str(e)

        return self._SendJson(req, ret)

    def _ValueDict(self, key, value):
        return {key : {"value" : value}}
    
    def _ParseCommand(self, req, store, cmdstr, timezone, viewStart, viewEnd):
        try:
            command = Command(cmdstr, timezone)
        except ValueError:
            return bongo.dragonfly.HTTP_BAD_REQUEST
        
        if command.GetType() != calcmd.NEW_APPT:
            return bongo.dragonfly.HTTP_INTERNAL_SERVER_ERROR

        jcal = command.GetJson()

        jsob = JsonObject()
        jsob["jcal"] = jcal

        occ = cal.PrimaryOccurrence(jcal, timezone)
        if occ is not None :
            jsob["occurrences"] = [ occ ]

        data = str(jsob)
        req.headers_out["Content-Length"] = str(len(data))
        req.content_type = "text/javascript"
        req.write(data)
        return bongo.dragonfly.OK

    def GetEventsJson (self, store, timezone, start, end) :
        events = store.Events(None, ["nmap.document", "nmap.event.calendars"],
                              start=start, end=end)

        return self._EventsToJson (events, timezone, start, end)
    
    def _EventsToJson (self, events, timezone, start, end) :
        array = JsonArray()
        for event in events :
            doc = event.props["nmap.document"].strip()

            jsob = JsonObject()
            jsob["bongoId"] = event.uid

            cals = []
            if event.props.has_key("nmap.event.calendars"):
                cals = event.props["nmap.event.calendars"].split()
            jsob["calendarIds"] = cals

            jcal = bongojson.ParseString(doc)
            jsob["jcal"] = jcal

            if start and end :
                occs = cal.Collect(jcal, start, end, timezone)
            else :
                occs = JsonArray()
                occs.append(cal.PrimaryOccurrence(jcal, timezone))
            if occs is not None :
                jsob["occurrences"] = occs
                array.append(jsob)

        return array
        
    def _SendEventsJson(self, req, events, timezone, start, end) :
        array = self._EventsToJson(events, timezone, start, end)
        data = str(array)
        req.headers_out["Content-Length"] = str(len(data))
        req.content_type = "text/javascript"
        req.write(data)
        return bongo.dragonfly.OK

    def _SendEventsIcs(self, req, events):
        wholeJsob = None;
        for event in events:
            doc = event.props["nmap.document"].strip()
            jsob = bongojson.ParseString(doc)
            if wholeJsob :
                wholeJsob = cal.Merge(wholeJsob, jsob)
            else :
                wholeJsob = jsob

        if wholeJsob :
            data = cal.JsonToIcal(wholeJsob)
        else :
            data = ""

        req.headers_out["Content-Length"] = str(len(data))
        req.content_type = "text/calendar"

        req.write(data)

        return bongo.dragonfly.OK

    def _ParseIcalTime(self, timestr) :
        year = int(timestr[0:4])
        month = int(timestr[4:6])
        day = int(timestr[6:8])

        if len(timestr) > 8 :
            hour = int(timestr[9:11])
            minute = int(timestr[11:13])
            second = int(timestr[13:15])
            return (datetime.date(year, month, day), datetime.time(hour, minute, second, 0, tzinfo=None))
        else :
            return (datetime.date(year, month, day), None)

    def _CompareOccs(self, a, b) :
        diff = cmp(a["dtstart"]["valueLocal"], b["dtstart"]["valueLocal"])
        if diff != 0 :
            return diff
        diff = cmp(a["dtend"]["valueLocal"], b["dtend"]["valueLocal"])
        if diff != 0 :
            return diff
        return cmp(a.get("summary", {}).get("value", ""), b.get("summary", {}).get("value", ""))
                   
    def _CollectOccs(self, events, timezone, start, end) :
        all = []
        for event in events :
            doc = event.props["nmap.document"].strip()

            jcal = bongojson.ParseString(doc)

            occs = cal.Collect (jcal, start, end, timezone)
            occs = bongojson.ToPy(occs)
            for occ in occs :
                occ["bongoid"] = event.uid
                occ["start"] = self._ParseIcalTime(occ["dtstart"]["valueLocal"])
                occ["end"] = self._ParseIcalTime(occ["dtend"]["valueLocal"])
                
            all.extend (occs)

        all.sort(self._CompareOccs)
        return all
        
    def _AddWeekHtml(self, req, html, baseuri, occs, thismonth, startdate) :
        html.append("<tr>")
        today = datetime.date.today()
        
        date = startdate
        oneday = datetime.timedelta(1)

        for i in range(0, 7) :
            dayid = "%04d%02d%02d" % (date.year, date.month, date.day)

            classes = []
            if date.month != thismonth :
                classes.append("offmonth")
            if i == 6 :
                classes.append("last")
            if date == today :
                classes.append("today")

            if classes == [] :
                html.append('<td id="d%s">' % (dayid))
            else :
                html.append('<td id="d%s" class="%s">' % (dayid, " ".join(classes)))

            html.append('<div class="date">%s</div>' % (date.day))

            untimed = []
            timed = []
            j = 0;
            while (j < len(occs)) :
                occ = occs[j]
                occStartDate = occ["start"][0]
                occStartTime = occ["start"][1]                
                occEndDate = occ["end"][0]
                occEndTime = occ["end"][1]

                if occEndDate < date or (occEndTime == None and occEndDate == date) :
                    occs.remove(occ)
                else :
                    if (occStartDate <= date) :
                        if occStartTime :
                            timed.append(occ)
                        else :
                            untimed.append(occ)
                    else :
                        break
                    j += 1

            html.append('<ul id="u%s" class="untimed-events">' % (dayid))
            for occ in untimed :
                html.append('<li><a href="%s-/%s:%s.html" onClick="return open_event(this);">%s</a></li>' % (baseuri,
                                                                     occ["bongoid"], occ["recurid"]["value"],
                                                                     occ.get("summary", {}).get("value", "")))
                          
            html.append('</ul><ul id="t%s" class="timed-events">' % (dayid))
            for occ in timed :
                startTime = occ["start"][1]
                endTime = occ["end"][1]
                
                # FIXME: time formatting prefs
                html.append('<li><a href="%s-/%s:%s.html" onClick="return open_event(this);">%s (%s - %s)' % (baseuri,
                                                                      occ["bongoid"], occ["recurid"]["value"],
                                                                      occ.get("summary", {}).get("value", ""),
                                                                      self._FormatTime(req, startTime), self._FormatTime(req, endTime)))

            html.append('</ul></td>')
            date = date + oneday
        return date

    def _GetMonthViewHtml(self, req, baseuri, events, timezone, year, month, start, end) :        
        occs = self._CollectOccs(events, timezone, start, end)
        
        thefirst = datetime.date(year, month, 1)
        delta = datetime.timedelta(thefirst.weekday())

        day = thefirst - delta

        html = []

        day = self._AddWeekHtml(req, html, baseuri, occs, month, day)
        day = self._AddWeekHtml(req, html, baseuri, occs, month, day)
        day = self._AddWeekHtml(req, html, baseuri, occs, month, day)
        day = self._AddWeekHtml(req, html, baseuri, occs, month, day)
        if day.month == month :
            day = self._AddWeekHtml(req, html, baseuri, occs, month, day)

        return "".join(html)

    def _GetCalendarTemplateValue(self, req, key, data) :
        ret = data["values"].get(key)
        if ret :
            return ret

        value = None
        if key == "calendar_grid" :
            value = self._GetMonthViewHtml(req,
                                           data["baseUri"],
                                           data["events"], data["timezone"],
                                           data["year"], data["month"],
                                           data["start"], data["end"])

        if value :
            data["values"][key] = value
            return value

        return ResourceHandler._GetTemplateValue(self, req, key, None)

    def _SendEventsHtml(self, store, req, rp, cal, timezone):
        eventProps = ["nmap.document", "nmap.event.calendars"]

        if rp.resource:
            splitResource = rp.resource.split('/')
            # For the moment we ignore the increment, static html is
            # always month view
            year = int(splitResource[1])
            month = int(splitResource[2])
        else :
            splitResource = None
            today = datetime.date.today()
            year = today.year
            month = today.month

        (start, end) = self._GetTimeRange("month", year, month)
            
        events = list(store.Events(cal, eventProps, query=rp.query, start=start, end=end))

        tmplData = {}

        tmplData["events"] = events
        tmplData["year"] = year
        tmplData["month"] = month
        tmplData["timezone"] = timezone
        tmplData["start"] = start
        tmplData["end"] = end
        values = {}
        tmplData["values"] = values

        if splitResource == None :
            baseUri = "events/"
        else :
            baseUri = ("../" * (len(splitResource)))

        tmplData["baseUri"] = baseUri

        if rp.authToken :
            token = "," + rp.authToken
        else :
            token = ""

        nextmonth = month + 1
        nextyear = year
        if nextmonth == 13 :
            nextmonth = 1
            nextyear += 1
        values["next_link"] = baseUri + "-/month/%02d/%02d.html%s" % (nextyear, nextmonth, token)
        lastmonth = month - 1
        lastyear = year
        if lastmonth == 0 :
            lastmonth = 12
            lastyear -= 1
        values["prev_link"] = baseUri + "-/month/%02d/%02d.html%s" % (lastyear, lastmonth, token)

        thedate = datetime.date(year, month, 15)
        values["month_name"] = self._T(req, thedate.strftime("%B"))
        values["year"] = thedate.strftime("%Y")
        values["calendar_name"] = rp.collection[0]

        tmpl = Template("month-view.html")

        req.content_type = "text/html"
        tmpl.send(req, self._GetCalendarTemplateValue, tmplData)
        
        return bongo.dragonfly.OK

    def _SendEventHtml(self, store, req, rp, bongoid, recurid, timezone):
        eventProps = ["nmap.document", "nmap.event.calendars"]
        
        event = store.Info(bongoid, eventProps)
        req.content_type = "text/plain"

        jcal = bongojson.ParseString(event.props["nmap.document"])
        occ = None
        if recurid :
            # Hack - assume that a recurrence hasn't moved more than a month from where it should be
            (recurdate, recurtime) = self._ParseIcalTime(recurid)
            if not recurtime :
                recurtime = datetime.time(0, 0, 0)
            recurdt = datetime.datetime(recurdate.year, recurdate.month, recurdate.day, recurtime.hour, recurtime.minute, recurtime.second)
            month = datetime.timedelta(31)
            occs = cal.Collect (jcal, recurdt - month, recurdt + month, timezone)
            occs = bongojson.ToPy(occs)
            if occs :
                for tmp in occs :
                    if tmp["recurid"]["value"] == recurid :
                        occ = tmp
                        break
                
        if not occ :
            occ = cal.PrimaryOccurrence (jcal, timezone)
            occ = bongojson.ToPy(occ)

        jcal = self._JsonToObj(event.props["nmap.document"])

        instance = None
        for component in jcal["components"] :
            compUid = component.get("uid", {}).get("value")
            compRecurid = component.get("recurid", {}).get("value")
            if not compRecurid :
                compRecurid = component.get("dtstart", {}).get("value")
            
            if compUid == occ["uid"] and compRecurid == recurid :
                instance = component
                break

        if not instance :
            raise HttpError(404, "Occurrence not found")
        
        values = {}

        values["summary"] = occ.get("summary", {}).get("value")
        values["location"] = instance.get("location", {}).get("value")
        values["note"] = instance.get("note", {}).get("value")
        (startdate, starttime) = self._ParseIcalTime(occ["dtstart"]["value"])
        (enddate, endtime) = self._ParseIcalTime(occ["dtend"]["value"])

        values["startdate"] = self._FormatDate (req, startdate)

        if starttime :
            values["starttime"] = self._FormatTime (req, starttime)

        values["enddate"] = self._FormatDate (req, enddate)

        if endtime :
            values["endtime"] = self._FormatTime (req, endtime)

        if startdate == enddate :
            if starttime and endtime :
                values["timerange"] = "%s %s-%s" % (self._FormatDate(req, startdate),
                                                    self._FormatTime(req, starttime),
                                                    self._FormatTime(req, endtime))
            else :
                values["timerange"] = self._FormatDate(req, startdate)
        else :
            if starttime and endtime :
                values["timerange"] = "%s %s - %s %s" % (self._FormatDate(req, startdate),
                                                         self._FormatTime(req, starttime),
                                                         self._FormatDate(req, enddate),
                                                         self._FormatTime(req, endtime))
            else :
                values["timerange"] = self._FormatDate(req, startdate)

        tmpl = Template("event.html")
        req.content_type="text/html"
        tmpl.send(req, self._GetTemplateValue, values)

        return bongo.dragonfly.OK
        
                
class CalendarsHandler(ResourceHandler):

    def _GetTypeFromProps(self, props):
        if props.has_key("url") or props.has_key("bongo.calendar.url"):
            return "web"
        return "personal"

    # fake acls on properties for now
    def _CalendarToJson(self, cal, show_all = True) :
        obj = { "name" : cal.filename }

        if show_all:
            if cal.props.has_key("bongo.calendar.color"):
                obj["color"] = cal.props["bongo.calendar.color"]

            if cal.props.has_key("bongo.calendar.username"):
                obj["username"] = cal.props["bongo.calendar.username"]

            obj["publish"] = cal.props.get("bongo.calendar.publish") == "1"
            
        if cal.props.has_key ("bongo.calendar.url"):
            if show_all:
                obj["url"] = cal.props["bongo.calendar.url"]
            else:
                obj["url"] = "restricted"

        obj["protect"] = cal.props.get("bongo.calendar.protect") == "1"
        obj["searchable"] = cal.props.get("bongo.calendar.searchable") == "1"

        if cal.props.get("nmap.access-control") :
            acl = CalendarACL(ACL(cal.props["nmap.access-control"]))
            summary = acl.Summarize()

            jsonAcl = []
            for address in summary["addresses"] :
                entry = summary["addresses"][address]
                hasWrite = (entry["rights"] & CalendarACL.Rights.Write) != 0
                jsonAcl.append((address, hasWrite))
            if len(jsonAcl) > 0 :
                obj["acl"] = jsonAcl

        return obj
    
    def _SendCalendars(self, store, req, rp):
        props = ["bongo.calendar.url",
                 "bongo.calendar.owner",
                 "bongo.calendar.color",
                 "bongo.calendar.publish",
                 "bongo.calendar.protect",
                 "bongo.calendar.searchable" ]

        cals = store.List("/calendars", props)

        jsob = []

        for cal in cals:
            if req.user == rp.user or cal.props.get("bongo.calendar.searchable") == "1":
                jsob.append((cal.uid, self._CalendarToJson(cal, req.user == rp.user)))
        
        return self._SendJson(req, jsob)

    def _SendCalendar(self, store, req, rp):
        props = ["bongo.calendar.url",
                 "bongo.calendar.owner",
                 "bongo.calendar.color",
                 "bongo.calendar.publish",
                 "bongo.calendar.protect",
                 "bongo.calendar.searchable",
                 "bongo.calendar.username",
                 "nmap.access-control"]
        cal = store.Info(rp.resource, props)
        obj = self._CalendarToJson(cal)
        return self._SendJson(req, obj)

    
    def do_GET(self, req, rp):
        store = self.GetStoreClient(req, rp)
        try:
            if rp.resource :
                return self._SendCalendar(store, req, rp)
            else :
                return self._SendCalendars(store, req, rp)
        finally:
            store.Quit()

    def do_DELETE(self, req, rp):
        req.log.debug("Deleting %s", rp.resource)

        store = self.GetStoreClient(req, rp)
        try:
            if rp.resource is not None:
                req.log.debug("Deleting doc %s", rp.resource)
                store.Delete(rp.resource)
                return bongo.dragonfly.OK
            else:
                return bongo.dragonfly.HTTP_NOT_FOUND
        finally:
            store.Quit()

    def _CreateCalendarObject(self, store, nameTemplate) :
        # FIXME: right now a "file already exists" is thrown as a "sql db error"
        # we only try 10 renames, in case it really throws a sql db error
        attempt = 1
        while attempt < 10 :
            name = nameTemplate
            if attempt > 1 :
                name = "%s %d" % (nameTemplate, attempt)
            try :
                bongoId = store.Write("/calendars", DocTypes.Calendar, "", filename = name)
                return (bongoId, name)
            except CommandError, e:
                if e.code == 5005 :
                    attempt += 1
                else :
                    raise
        raise RuntimeError("DB library error")
            
    def _ApplyProp(self, store, bongoId, jsob, jsonName, storeName, default = None) :
        propval = jsob.get(jsonName, default)
        if propval is not None:
            if propval is True:
                propval = "1"
            elif propval is False:
                propval = "0"
            store.PropSet(bongoId, storeName, propval)

    def _EnsureSharing(self, store) :
        acl = store.GetACL("/calendars")
        acl.Grant("all", "list")
        store.SetACL("/calendars", acl)

    def _ApplyProperties(self, req, store, bongoId, jsob, isNew) :
        if jsob.has_key("url"):
            # Subscribed calendar
            self._ApplyProp(store, bongoId, jsob, "url", "bongo.calendar.url")
            self._ApplyProp(store, bongoId, jsob, "username", "bongo.calendar.username")
            self._ApplyProp(store, bongoId, jsob, "password", "bongo.calendar.password")
            self._ApplyProp(store, bongoId, jsob, "localowner", "bongo.calendar.owner")
            self._ApplyProp(store, bongoId, jsob, "localid", "bongo.calendar.uid")
            self._ApplyProp(store, bongoId, jsob, "token", "bongo.calendar.token")
            self._ApplyProp(store, bongoId, jsob, "searchable", "bongo.calendar.searchable", "0")
        else:
            self._ApplyProp(store, bongoId, jsob, "publish", "bongo.calendar.publish", "0")
            if jsob.get("publish"):
                self._ApplyProp(store, bongoId, jsob, "protect", "bongo.calendar.protect", "0")
                if jsob.get("protect"):
                    self._ApplyProp(store, bongoId, jsob, "username", "bongo.calendar.username")
                self._ApplyProp(store, bongoId, jsob, "searchable", "bongo.calendar.searchable", "0")
            else:
                store.PropSet(bongoId, "bongo.calendar.protect", "0")
                store.PropSet(bongoId, "bongo.calendar.searchable", "0")

        self._ApplyProp(store, bongoId, jsob, "color", "bongo.calendar.color")

    def _UpdateACL(self, store, bongoId, jsob, acl):
        oldAclSummary = acl.Summarize()
        
        acl.Clear()
        
        if jsob.has_key("url"):
            if jsob.get("searchable"):
                self._EnsureSharing(store)
                acl.SetPublic(CalendarACL.Rights.ReadProps)
        elif jsob.get("publish"):
            if jsob.get("protect"):
                acl.AddPassword(jsob.get("username"),
                                jsob.get("password"),
                                CalendarACL.Rights.Read)

            if jsob.get("acl") :
                jsobAcl = jsob.get("acl")
                for entry in jsobAcl :
                    address, write = entry
                    info = oldAclSummary["addresses"].get(address)
                    if info :
                        password = info["password"]
                    else :
                        password = None

                    rights = CalendarACL.Rights.Read
                    if (write) :
                        rights = rights | CalendarACL.Rights.Write

                    acl.Share(address, rights, password)
                    
            if not jsob.get("protect") and not jsob.get("acl") :
                acl.SetPublic(CalendarACL.Rights.Read)
                
            acl.SetPublic(acl.GetPublic() | CalendarACL.Rights.ReadProps)

        store.SetACL(bongoId, acl.GetACL())

    def _ValidateUpdateRequest(self, jsob, doc=None, acl=None) :
        if not jsob.has_key("name"):
            raise HttpError(400, "A calendar name is required.")

        username = jsob.get("username")
        password = jsob.get("password")
        
        if jsob.get("protect") :
            if not username :
                raise HttpError(400, "No username specified.")

            if not password :
                # If the username matches the existing username, a password doesn't need
                # to be specified, the old one will still be used.  Otherwise the password
                # is required.
                if acl :
                    oldPasswords = acl.GetPasswords()

                    if oldPasswords.has_key(username) :
                        password = oldPasswords[username]
                        jsob["password"] = password

            if not password :
                raise HttpError(400, "No password specified.")

        if jsob.has_key("url") :
            if username and not password :
                # If the username matches the existing username, a password doesn't need
                # to be specified, the old one will still be used.  Otherwise the password
                # is required.

                if doc :
                    docUsername = doc.props.get("bongo.calendar.username")
                    docPassword = doc.props.get("bongo.calendar.password")
                    
                    if docUsername == username :
                        password = docPassword
                    jsob["password"] = password

                if not password :
                    raise HttpError(400, "No password specified")

            
    def _RenameSimilar(self, store, bongoId, nameTemplate) :
        # FIXME: right now a "file already exists" is thrown as a "sql db error"
        # we only try 10 renames, in case it really throws a sql db error
        attempt = 1
        while attempt < 10 :
            name = nameTemplate
            if attempt > 1 :
                name = "%s %d" % (nameTemplate, attempt)
            try :
                store.Move(bongoId, "/calendars", name)
                return name
            except CommandError, e:
                if e.code == 5005 :
                    attempt += 1
                else :
                    raise
        raise RuntimeError("DB library error")

    def _CreateCalendar(self, req, rp, jsob, importFile=None) :
        self._ValidateUpdateRequest(jsob)
        
        store = self.GetStoreClient(req, rp)
        try:
            (bongoId, name) = self._CreateCalendarObject(store, jsob["name"])
            jsob["name"] = name
            self._UpdateACL(store, bongoId, jsob, CalendarACL(ACL()))
            self._ApplyProperties(req, store, bongoId, jsob, True)
            if jsob.has_key("url") and not jsob.has_key("localowner"):
                # Run the collector
                username = jsob.get("username")
                password = jsob.get("password")
                    
                msgapi.IcsSubscribe(rp.user,
                                    jsob["name"],
                                    jsob.get("color", ""),
                                    jsob["url"],
                                    username,
                                    password)

                # hack - The collector sets the name from the ics in a property
                # on initial creation, use this name as a template for the real
                # filename

                doc = store.Info(bongoId, ["bongo.calendar.icsname"])
                icsName = doc.props.get("bongo.calendar.icsname")
                if icsName :
                    # Ignore db errors, because this step isn't strictly necessary
                    try :
                        name = self._RenameSimilar(store, bongoId, icsName)
                    except RuntimeError, e:
                        if str(e) != "DB library error" :
                            raise
                
            elif importFile is not None :
                msgapi.ImportIcs(importFile,
                                 rp.user,
                                 jsob["name"],
                                 jsob.get("color", ""))
            return (bongoId, name)
        
        finally:
            store.Quit()

    def _UpdateCalendar(self, req, rp, jsob) :
        store = self.GetStoreClient(req, rp)
        try:
            bongoId = rp.resource

            props = ["bongo.calendar.url", "bongo.calendar.owner", "bongo.calendar.username", "bongo.calendar.password"]
            doc = store.Info(bongoId, props)
            acl = CalendarACL(ACL(doc.props.get("nmap.access-control", "")))
            
            if self._GetTypeFromProps(doc.props) != self._GetTypeFromProps(jsob):
                raise RuntimeError ("Cannot change the type of a calendar")

            self._ValidateUpdateRequest(jsob, doc, acl)
                            
            if doc.filename != jsob["name"] :
                store.Move(bongoId, "/calendars", jsob["name"])
            self._UpdateACL(store, bongoId, jsob, acl)
            self._ApplyProperties(req, store, bongoId, jsob, False)

            if jsob.has_key("url") and not jsob.has_key("localowner") :
                # Run the collector
                url = jsob.get("url")
                username = jsob.get("username", None)
                password = jsob.get("password", None)

                if (doc.props.get("bongo.calendar.url") != url or
                    doc.props.get("bongo.calendar.username") != username or
                    doc.props.get("bongo.calendar.password") != password):
                    msgapi.IcsSubscribe(rp.user,
                                        jsob["name"],
                                        jsob["color"],
                                        url,
                                        username,
                                        password)       
        finally:
            store.Quit()
        

    def _Val(self, fields, name) :
        if fields.has_key(name) and fields[name].value != "" :
            return fields[name].value
        else :
            return None

    def _FieldsToJson(self, fields) :
        obj = { "method": self._Val(fields, "method") }
        params = {}
        params["name"] = self._Val(fields, "calendar-name")
        params["color"] = self._Val(fields, "calendar-color")

        calType = self._Val(fields, "calendar-type")
        if calType == "personal" or calType == "import":
            params["publish"] = self._Val(fields, "sharing-publish") == "on"
            params["searchable"] = self._Val(fields, "sharing-searchable") == "on"
            params["protect"] = self._Val(fields, "sharing-protect") == "on"
            params["username"] = self._Val(fields, "sharing-username")
            params["password"] = self._Val(fields, "sharing-password")
        if calType == "web" :
            params["url"] = self._Val(fields, "web-url")
            params["searchable"] = self._Val(fields, "web-searchable") == "on"
            params["protect"] = self._Val(fields, "web-protect") == "on"
            params["username"] = self._Val(fields, "web-username")
            params["password"] = self._Val(fields, "web-password")
        obj["params"] = [ params ]
        return obj

    def do_PUT(self, req, rp) :
        length = int(req.headers_in["Content-Length"])
        jsob = self._JsonToObj(req.read(length))

        self._FindLocalCalendar(req, jsob)

        try :
            self._UpdateCalendar(req, rp, jsob)
            return self._SendJson(req, {"result":  "Success" })
        except RuntimeError, e :
            return self._SendJson(req, {"error":  str(e) } )

    def _FindLocalCalendar(self, req, jsob) :
        if not jsob.has_key("url") :
            return

        localUrl = self._LocalUrl(req, jsob.get("url"))

        if localUrl :
            pieces = localUrl.split("/")
            if len(pieces) >= 3 :
                localOwner = urllib.unquote(pieces[0])
                localCalendar = urllib.unquote(pieces[2])
                # Try to find the local calendar
                filepieces = pieces[len(pieces) - 1].split(',', 2)
                if len(filepieces) > 1 :
                    token = urllib.unquote(filepieces[1])
                else :
                    token = None

                otherStore = StoreClient(req.user, localOwner, authToken=token, authCookie=req.authCookie)
                try :
                    doc = otherStore.Info("/calendars/%s" % (localCalendar))
                    if doc :
                        localCalendarId = doc.uid
                finally  :
                    otherStore.Quit()

                if localCalendarId :
                    jsob["localowner"] = localOwner
                    jsob["localid"] = localCalendarId
                    if token :
                        jsob["token"] = token
                else :
                    raise RuntimeError("Calendar not found")
                    
    def do_POST(self, req, rp):
        contentType = req.headers_in["Content-Type"]
        if contentType.startswith("multipart/form-data") :
            fields = req.form
            jsob = self._FieldsToJson(fields)
            importFile = fields["import-file"].file
        else :
            length = int(req.headers_in["Content-Length"])
            jsob = self._JsonToObj(req.read(length))
            importFile = None



        ret = {"id" : jsob.get("id"),
               "error" : None,
               "result" : None}

        method = jsob["method"]

        if method == "createCalendar" :
            self._FindLocalCalendar(req, jsob["params"][0])
            try :
                (bongoId, name) = self._CreateCalendar(req, rp, jsob["params"][0], importFile)
                ret["result"] = [bongoId, name]
            except RuntimeError, e :
                ret["error"] = str(e)

            if contentType == "multipart/form-data" :
                # override since it's in a form post
                req.content_type = "text/plain"

            return self._SendJson(req, ret)

        elif method == "updateCalendar" :
            self._FindLocalCalendar(req, jsob["params"][0])
            try :
                self._UpdateCalendar(req, rp, jsob["params"][0])
                ret["result"] = "Success"
            except RuntimeError, e :
                ret["error"] = str(e)

            if contentType == "multipart/form-data" :
                # override since it's in a form post
                req.content_type = "text/plain"

            return self._SendJson(req, ret)
