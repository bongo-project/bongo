from bongo.commonweb.ApacheLogHandler import ApacheLogHandler, RequestLogProxy
from HawkeyePath import HawkeyePath
from bongo.commonweb.HttpError import HttpError

import Auth
import bongo.commonweb
import bongo.commonweb.BongoFieldStorage as BongoFieldStorage
import bongo.commonweb.BongoUtil as BongoUtil
import bongo.commonweb.BongoSession as BongoSession

# Add a handler so messages from python's logging module go to apache.
# We don't use that module in the Bongo server proper, because we need
# to log to apache requests directly
import logging
logging.getLogger('').addHandler(ApacheLogHandler())
logging.root.setLevel(logging.DEBUG)

def handler(req):
    req.log = RequestLogProxy(req)
    
    req.log.debug("Request: %s", req)

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
        
        req.log.debug("Got handler and stuff. Getting session...")
        
        req.session = BongoSession.Session(req)
        req.session.load()
        
        req.log.debug("Checking we need auth:")
        
        if handler.NeedsAuth(rp):
            req.log.debug("Yup!")
            auth = bongo.hawkeye.Auth.authenhandler(req)
            if auth != bongo.commonweb.HTTP_OK:
                target = "/admin/login"
                BongoUtil.redirect(req, target)
                return bongo.commonweb.OK
        else:
            req.log.debug("Nope.")

        req.log.debug("request for %s (handled by %s)" % (req.path_info, handler))

        mname = rp.action + "_" + req.method
        hackymethod = False

        req.log.debug("Doing attr check on %s (%s)" % (handler, mname))
        
        if not hasattr(handler, mname):
            req.log.debug("Handler: %s", handler)
            req.log.debug("RP: %s", rp)
            if rp.view == "agents":
                # Special case for agents view
                hackymethod = True
                if req.method == "POST":
                    mname = "saveConfig"
                else:
                    mname = "showConfig"
            else:
                req.log.debug("%s has no %s" % (handler, mname))
                return bongo.commonweb.HTTP_NOT_FOUND

        req.log.debug("Getting attributes...")
        
        method = getattr(handler, mname)
        if hackymethod:
            req.log.debug("Running new way.")
            ret = method(rp.action, req, rp)
        else:
            req.log.debug("Running normal way.")
            ret = method(req, rp)

        if ret is None:
            req.log.debug("Handler %s returned invalid value: None" % handler)
            return bongo.commonweb.OK

        return ret
    except HttpError, e:
        req.log.debug("Error, yay!")
        req.log.debug(str(e), exc_info=1)
        return e.code
