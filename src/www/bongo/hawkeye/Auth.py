import bongo.dragonfly.Auth
import bongo.dragonfly.BongoSession as BongoSession

def Login(req):
    if not req.form:
        return bongo.dragonfly.HTTP_UNAUTHORIZED

    if not req.form.has_key("bongo-username") \
       or not req.form.has_key("bongo-password"):
        return bongo.dragonfly.HTTP_UNAUTHORIZED

    credUser = req.form["bongo-username"].value
    credPass = req.form["bongo-password"].value

    if not bongo.dragonfly.Auth.CheckUserPass(credUser, credPass):
        req.log.debug("invalid auth credentials")
        if req.session:
            req.session.invalidate()
        return False

    req.session["credUser"] = credUser
    req.session["credPass"] = credPass
    req.log.debug("updated session: %s", str(req.session))

    return True

def AcceptCredentials(req):
    req.log.debug("loaded session: %s", str(req.session))

    credUser = req.session.get("credUser")
    credPass = req.session.get("credPass")

    if not bongo.dragonfly.Auth.CheckUserPass(credUser, credPass):
        req.log.info("invalid auth credentials")
        req.session.invalidate()
        return False

    return True

def authenhandler(req):
    if AcceptCredentials(req):
        req.log.debug("successful auth")
        return bongo.dragonfly.HTTP_OK

    return bongo.dragonfly.HTTP_UNAUTHORIZED
