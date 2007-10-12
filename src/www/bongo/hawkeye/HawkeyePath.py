import os

import urllib
import bongo.commonweb
from bongo.commonweb.BongoSession import BongoSession
from bongo.commonweb.HttpError import HttpError

import RootView
import AgentView
import AntispamView
import BackupView
import ServerView
import SystemView

views = {
    "root" : RootView.RootHandler(),
    "agents" : AgentView.AgentHandler(),
    "backup" : BackupView.BackupHandler(),
    "system" : SystemView.SystemHandler(),
    "server" : ServerView.ServerHandler()
    }

class HawkeyePath:
    def __init__(self, req):
        self.req = req

        self.view = self.action = self.path = None

        opts = req.get_options()
        hawkeye_root = opts.get("HawkeyeUriRoot")
        if hawkeye_root is None:
            raise HttpError(bongo.commonweb.HTTP_INTERNAL_SERVER_ERROR,
                            "HawkeyeUriRoot not specified")

        hawkeye_tmpl_root = opts.get("HawkeyeTmplRoot")
        if hawkeye_tmpl_root is None:
            raise HttpError(bongo.commonweb.HTTP_INTERNAL_SERVER_ERROR,
                            "HawkeyeTmplRoot not specified")

        path = self.orig = req.uri[len(hawkeye_root):]

        parts = map(urllib.unquote, path.strip("/").split("/"))

        if len(parts) > 0 and parts[0] == "":
            parts.pop(0)

        if len(parts) > 0 and views.has_key(parts[0]):
            self.view = parts.pop(0)
        else:
            self.view = "root"

        if len(parts) > 0:
            self.action = parts.pop(0)
        else:
            self.action = "index"

        self.path = "/".join(parts)
        self.tmplPath = os.path.join(hawkeye_tmpl_root, self.view)
        self.tmplRoot = hawkeye_tmpl_root
        self.tmplUriRoot = hawkeye_root

        req.session = None

        req.log.debug(str(self))

    def __str__(self):
        parts = []
        for part in ("view", "action", "path"):
            parts.append("%s: %s" % (part, self.__dict__.get(part)))

        return "%s (%s)" % (object.__str__(self), ", ".join(parts))

    def GetHandler(self):
        global views

        if views.has_key(self.view):
            handler = views[self.view]
        else:
            raise HttpError(bongo.commonweb.HTTP_NOT_FOUND)

        handler.ParsePath(self)

        return handler
