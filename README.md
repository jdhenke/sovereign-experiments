# Sovereign Server Experiments

This repository contains the different paths a sovereign server could take from its initial starting point of
accepting all patches through each of the six experiments presented in the
[main sovereign repository](https://github.com/jdhenke/sovereign).

There is an `exp-*` branch for each experiment. The [network](https://github.com/jdhenke/sovereign-experiments/network)
view may be helpful in understanding the shape of things, or using `git log --graph` e.g.

```
$ git log --graph --oneline --all
* eef3b22 (origin/exp-6, exp-6) Revert "Lock in revertability"
* 0c9480c Lock in revertability
* 6979d0d Fix restart
| * 418febc (origin/exp-5, exp-5) Revert "add easter egg"
| * 2658030 add easter egg
| * 6154f88 Revert "Bootstrap shell server"
| * 1f498d1 Bootstrap shell server
|/  
| * ecf849f (origin/exp-4, exp-4) Break server
| * 0fd51fa Revert "Test patches to ensure server can still start"
| * 47526cb Add easter egg
| * ca28716 Test patches to ensure server can still start
|/  
| * 810d7c4 (origin/exp-3, exp-3) Log patches instead of applying them.
|/  
| * 0de96c0 (origin/exp-2, exp-2) Delete everything
|/  
| * 6e71469 (origin/exp-1, exp-1) Never admit any patch
|/  
* cf5c72c (HEAD -> master, origin/master) Admit any patch
```

**Note**: Some patches proposed in the tutorial were not accepted by the sovereign server, and so would not appear here,
because this is the _server_'s repository after all the experiments, **not** the client's.
