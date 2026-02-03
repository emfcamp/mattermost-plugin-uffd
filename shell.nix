{ pkgs ? import <nixpkgs> { } }:

pkgs.mkShell {
  packages = with pkgs; [
    gnumake
    go

    # Useful for doing... admin stuff
    mmctl

    # The Node Dot J S ecosystem
    nodejs
    python3
    stdenv
    pkg-config

    autoconf  # optipng/gifsicle
    automake  # optipng/gifsicle
    libtool  # optipng/gifsicle
    zlib  # optipng/gifsicle
    nasm  # optipng/gifiscle

    pixman  # node-canvas
    cairo  # node-canvas
    librsvg  # node-canvas
    libjpeg  # node-canvas
    pango  # node-canvas
  ];

  shellHook = ''
    export LD=$CC

    test -e $HOME/.cache/mmlocal.sock && export MM_LOCALSOCKETPATH=$HOME/.cache/mmlocal.sock
    test -e /var/tmp/mattermost_local.socket && export MM_LOCALSOCKETPATH=/var/tmp/mattermost_local.socket

    if [[ ! -z "''${MM_LOCALSOCKETPATH}" ]]; then
      export MMCTL_LOCAL=true
      export MMCTL_LOCAL_SOCKET_PATH=''${MM_LOCALSOCKETPATH}
    fi

    echo "Set MM_SERVICESETTINGS_ENABLEDEVELOPER=1 to build only for local arch"
    echo
    echo "Useful commands:"
    echo " - make"
    echo " - make test"
    echo " - make watch"
    echo
  '';
}
