#!/bin/bash

VZIC=../import/vzic/vzic
IMPORTTZ=../src/libs/cal/bongo-import-tz

if test -z $1; then
    echo "usage: update-zoneinfo.sh olsonpath"
    exit
fi

OLSONDIR=$1

if test ! -x $VZIC; then
    echo "vzic has not been built.  Build it in ../import/vzic"
    exit
fi

if test ! -x $IMPORTZ; then
    echo "bongo-import-tz has not been built.  Build it in ../src/libs/cal"
    exit
fi

rm -rf *.zone

mkdir ics-tmp

$VZIC --pure --output-dir ics-tmp --olson-dir $OLSONDIR


rm generated.tznames
find ics-tmp -name \*.ics -type f -print0 | xargs -0 --replace ../src/libs/cal/bongo-import-tz {} .
sort generated.tznames > generated.sort
mv generated.sort generated.tznames

echo "## This file is autogenerated by update-zoneinfo.sh" > Bongo.rules
echo "nobase_dist_pkgdata_DATA +== \\" >> Bongo.rules

ls *.zone | sed -e 's^.*^\tzoneinfo/& \\^' >> Bongo.rules

printf "\tgenerated.tznames \\" >> Bongo.rules
printf "\n\tamerica.tznames \\" >> Bongo.rules

printf "\n\t\$(NULL)\n" >> Bongo.rules
printf "\nzoneinfo/all:\n" >> Bongo.rules
printf "\nzoneinfo/clean: clean\n" >> Bongo.rules

rm -rf ics-tmp
