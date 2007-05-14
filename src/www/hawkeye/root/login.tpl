<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
    <title>Bongo Web Administration » Login</title>
	<link rel="icon" href="/img/bongo-favicon.ico" type="image/ico">
	<link rel="icon" href="/img/bongo-favicon.png" type="image/png">
    <meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
    <style type="text/css" media="screen">
        @import url(/css/admin.css);
    </style>
</head>
<body onload="document.forms[0].elements[0].focus();">
<div id="login">
    <form method="post">
        <input type="text" name="bongo-username" class="username inputbox" /><br />
        <input type="password" name="bongo-password" class="password inputbox"/><br />
        <select name="lang" class="lang">
            <option value="en">English</option>
            <!-- <option value="fr">Français</option> -->
        </select>
        <div class="submit"><span class="button"><button type="submit" value="Log in">Log in</button></span></div>
    </form>
</div>
</body>
</html>
