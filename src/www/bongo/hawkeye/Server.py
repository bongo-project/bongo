from mod_python import apache, util
from bongo.dragonfly.ApacheLogHandler import ApacheLogHandler, RequestLogProxy
from HawkeyePath import HawkeyePath
from bongo.dragonfly.HttpError import HttpError

import Auth
import bongo.dragonfly
import bongo.dragonfly.BongoFieldStorage as BongoFieldStorage
import bongo.dragonfly.BongoUtil as BongoUtil

# Add a handler so messages from python's logging module go to apache.
# We don't use that module in the Bongo server proper, because we need
# to log to apache requests directly
import logging
logging.getLogger('').addHandler(ApacheLogHandler())
logging.root.setLevel(logging.DEBUG)

def handler(req):
    req.log = RequestLogProxy(req)

    # if the req is multipart/form-data, decode its contents now, so
    # we can use the authentication field in the auth handler.
    req.form = None
    reqtype = req.headers_in.get("Content-Type", None)
    if reqtype and (reqtype.startswith("application/x-www-form-urlencoded")
                    or reqtype.startswith("multipart/form-data")):
        req.form = BongoFieldStorage.get_fieldstorage(req)

    try:
        rp = HawkeyePath(req)
        handler = rp.GetHandler()
        
        if handler.NeedsAuth(rp):
            auth = bongo.hawkeye.Auth.authenhandler(req)
            if auth != bongo.dragonfly.HTTP_OK:
                target = "/admin/login"
                BongoUtil.redirect(req, target)
                return bongo.dragonfly.OK

        req.log.debug("request for %s (handled by %s)", req.path_info, handler)

        mname = rp.action + "_" + req.method

        if not hasattr(handler, mname):
            req.log.debug("%s has no %s", handler, mname)
            return bongo.dragonfly.HTTP_NOT_FOUND

        method = getattr(handler, mname)
        ret = method(req, rp)

        if ret is None:
            req.log.error("Handler %s returned invalid value: None" % handler)
            return bongo.dragonfly.OK

        return ret
    except HttpError, e:
        req.log.debug(str(e), exc_info=1)
        return e.code
