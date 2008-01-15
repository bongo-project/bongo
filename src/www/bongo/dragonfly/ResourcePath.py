import urllib
import bongo.commonweb
from bongo.commonweb.HttpError import HttpError

import AddressbookView
import CalendarView
import MailView
import PreferencesView
import SummaryView

class ResourcePath:
    _views = {"addressbook" : 1,
              "calendar" : 1, 
              "mail" : 1,
              "preferences" : 1,
              "search" : 1,
              "summary" : 1}

    def __init__(self, req):
        self.req = req

        self.user = self.view = self.collection = self.handler = self.page = \
                    self.query = self.resource = self.extension = \
                    self.rawResource = self.authToken = None

        self.handlerData = {}

        opts = req.get_options()
        dragonfly_root = opts.get("DragonflyUriRoot")
        if dragonfly_root is None:
            raise HttpError(bongo.commonweb.HTTP_INTERNAL_SERVER_ERROR,
                            "DragonflyUriRoot not specified")

        path = self.orig = req.uri[len(dragonfly_root):]

        parts = map(urllib.unquote, path.split("/"))

        index = 1

        if len(parts) < 3:
            raise HttpError(404)

        # strip the auth token from the last part
        tmp = parts[-1].rfind(",")
        if tmp != -1:
            self.authToken = parts[-1][tmp+1:]
            parts[-1] = parts[-1][:tmp]

        self.rawResource = parts[-1]

        # strip the extension from the last part
        tmp = parts[-1].rfind(".")
        if tmp != -1:
            self.extension = parts[-1][tmp+1:]
            parts[-1] = parts[-1][:tmp]

        self.user = parts[index]
        index += 1

        if self._views.has_key(parts[index]):
            self.view = parts[index]
            index += 1
        else:
            raise HttpError(404, "Unrecognized view: %s" % parts[index])

        count = self._views[self.view]

        self.collection = []
        for i in range(count):
            if index >= len(parts):
                raise HttpError(404, "Not enough collection components")
            self.collection.append(parts[index])
            index += 1

        if index >= len(parts):
            raise HttpError(404, "No handler found")

        self.handler = parts[index]
        index += 1

        # strip the query, if found, from the handler
        tmp = self.handler.find("(")
        if tmp != -1:
            self.query = self.handler[tmp+1:]
            if self.query.endswith(")"):
                self.query = self.query[:-1]
            self.handler = self.handler[:tmp]

        # strip the args
        if index < len(parts):
            self.args = parts[index]
            index += 1

            if not self.args == '-':
                if not self.args.startswith("page"):
                    raise HttpError(404, "Page string doesn't start with \"page\"")
                self.page = int(self.args[4:])

        count = len(parts)-index
        if count == 0:
            return

        resources = []
        for i in range(count):
            resources.append(parts[index])
            index += 1
        self.resource = "/".join(resources)

    def __str__(self):
        parts = []
        for part in ("orig", "user", "view", "collection", "handler", "page",
                     "query", "resource", "extension", "authToken",
                     "rawResource"):
            parts.append("%s: %s" % (part, self.__dict__.get(part)))

        return "%s (%s)" % (object.__str__(self), ", ".join(parts))

    def GetHandler(self):
        handler = None
        if self.view == "addressbook" and self.handler == "contacts":
            handler = AddressbookView.ContactsHandler()
        elif self.view == "calendar" and self.handler == "events":
            handler = CalendarView.EventsHandler()
        elif self.view == "calendar" and self.handler == "calendars":
            handler = CalendarView.CalendarsHandler()
        elif self.view == "mail" and self.handler == "contacts":
            handler = MailView.ContactsHandler()
        elif self.view == "mail" and self.handler == "tome":
            handler = MailView.ToMeHandler()
        elif self.view == "mail" and self.handler == "subscriptions":
            handler = MailView.SubscriptionsHandler()
        elif self.view == "mail" and self.handler == "conversations":
            handler = MailView.ConversationsHandler()
        elif self.view == "preferences" and self.handler == "dragonfly":
            handler = PreferencesView.DragonflyHandler()
        elif self.view == "preferences" and self.handler == "rules":
            handler = PreferencesView.RulesHandler()
        elif self.view == "summary" and self.handler == "summary":
            handler = SummaryView.SummaryHandler()
        elif self.view == "sendlog":
            handler = SummaryView.LogHandler()

        if handler is None:
            raise HttpError(bongo.commonweb.HTTP_NOT_FOUND)

        handler.ParsePath(self)

        return handler
