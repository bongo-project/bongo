import os
import time
import random
import pickle
import md5

import bongo.Xpl as Xpl

import BongoCookie

class BongoSession(dict):
    def __init__(self, req, lock=1):
        self.req = req

        cookie = BongoCookie.get_cookies(req).get('sid', None)
        if cookie is None:
            digest = md5.new(req.oldreq.address_string())
            digest.update(str(time.time()))
            self.sid = digest.hexdigest()
            self.unsaved = True

            # 'path' should be the application path. e.g. '/bongoadmin'
            BongoCookie.add_cookie(req, 'sid', self.sid, path='/',
                                  expires=time.time()+3600)
        else:
            self.sid = cookie.value
            self.unsaved = False

        self.invalid = False

        sessionDir = os.path.join(Xpl.DEFAULT_CACHE_DIR, "sessions")
        self.path = os.path.join(sessionDir, "%s.session" % self.sid)

        omask = os.umask(0077)
        if not os.path.exists(sessionDir):
            os.makedirs(sessionDir)
        os.umask(omask)

    def save(self):
        req = self.req

        if self.invalid:
            req.log.error("trying to save an invalid session!")
            return

        req.log.debug("saving session: %s", self.sid)

        omask = os.umask(0077)

        f = open(self.path, 'w')
        pickle.dump(dict(self), f)
        f.close()

        os.umask(omask)

        self.unsaved = False

    def load(self):
        req = self.req

        if self.invalid:
            req.log.error("trying to load an invalid session!")
            return

        req = self.req

        if self.unsaved:
            return

        req.log.debug("trying to load session from %s", self.path)

        try:
            f = open(self.path)
            self.update(pickle.load(f))
            f.close()
        except IOError, e:
            req.log.debug("error loading session: %s", str(e), exc_info=1)
            self._deleteCookie()

    def invalidate(self):
        self.req.log.debug("invalidating session: %s", self.sid)

        self._deleteCookie()
        if os.path.exists(self.path):
            os.remove(self.path)

        self.invalid = True

    def unlock(self):
        pass

    def _deleteCookie(self):
        # delete the cookie by setting its expiration date to a week ago
        BongoCookie.add_cookie(self.req, 'sid', self.sid, path='/',
                              expires=time.time()-604800)

try:
    from mod_python import Session as ApacheSession
    Session = ApacheSession.Session
except ImportError:
    Session = BongoSession
