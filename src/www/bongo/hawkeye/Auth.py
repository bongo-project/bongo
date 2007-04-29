import bongo.dragonfly.Auth
from bongo.store.StoreClient import StoreClient
import bongo.dragonfly.BongoSession as BongoSession
import time

def Login(req):
    if not req.form:
        return bongo.dragonfly.HTTP_UNAUTHORIZED

    if not req.form.has_key("bongo-username") \
       or not req.form.has_key("bongo-password"):
        return bongo.dragonfly.HTTP_UNAUTHORIZED

    credUser = req.form["bongo-username"].value
    credPass = req.form["bongo-password"].value

    store = None
    try:
        store = StoreClient(credUser, credUser, authPassword=credPass)
    finally:
        if store:
            store.Quit()
        else:
            req.session.invalidate()
            return False

    req.session["credUser"] = credUser
    req.session["credPass"] = credPass
    req.session.save()
    return True

def AcceptCredentials(req):
    credUser = req.session.get("credUser")
    credPass = req.session.get("credPass")
    
    if credUser==None and credPass==None:
        # no session
        return False

    client = None
    try:
        client = StoreClient(credUser, credUser, authPassword=credPass)
    finally:
        if client:
            client.Quit()
            return True

    return False

def authenhandler(req):
    if AcceptCredentials(req):
        req.log.debug("successful auth")
        return bongo.dragonfly.HTTP_OK

    return bongo.dragonfly.HTTP_UNAUTHORIZED
